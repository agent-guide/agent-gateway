package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/agent-guide/agent-gateway/internal/agwctl/cliauthstore"
	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	_ "github.com/agent-guide/agent-gateway/pkg/cliauth/authenticator"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// ── cliauth ───────────────────────────────────────────────────────────────────

var cliauthCmd = &cobra.Command{
	Use:   "cliauth",
	Short: "Manage gateway CLI auth login flows and local authenticator support",
}

// ── cliauth login ─────────────────────────────────────────────────────────────

var (
	loginAuthenticator    string
	loginProviderID       string
	loginScope            string
	loginLabel            string
	loginCallbackPort     int
	loginNoBrowser        bool
	loginDeviceFlow       bool
	loginTransportProfile string
)

var cliauthLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Run an interactive CLI auth login flow and save the credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		authenticator, err := validateLoginAuthenticator(cmd, loginAuthenticator)
		if err != nil {
			return err
		}

		credMgr, err := cliauthstore.New(cliauthstore.Config{
			BaseURL:  globalGatewayAddr,
			Username: gwUser,
			Password: gwPassword,
		})
		if err != nil {
			return err
		}
		refresher := cliauth.NewAutoRefresher(credMgr, nil)
		if err := refresher.Load(context.Background()); err != nil {
			return err
		}
		if strings.TrimSpace(loginProviderID) == "" {
			return fmt.Errorf("provider-id is required")
		}
		adminClient := adminclient.New(adminclient.Config{
			BaseURL:  globalGatewayAddr,
			Username: gwUser,
			Password: gwPassword,
		})
		providerCfg, err := adminClient.GetProvider(context.Background(), strings.TrimSpace(loginProviderID))
		if err != nil {
			return err
		}

		auth, err := cliauth.NewAuthenticator(authenticator)
		if err != nil {
			return err
		}
		if err := cliauth.ApplyAuthenticatorConfigOverrides(auth, cliauth.AuthenticatorConfig{
			CallbackPort:     loginCallbackPort,
			NoBrowser:        loginNoBrowser,
			DeviceFlow:       loginDeviceFlow,
			TransportProfile: strings.TrimSpace(loginTransportProfile),
		}); err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		reporter := &cliauthStatusReporter{}
		cred, err := auth.Login(ctx, cliauth.LoginRequest{
			ProviderID: strings.TrimSpace(loginProviderID),
			Scope:      strings.TrimSpace(loginScope),
		}, reporter)
		if err != nil {
			return err
		}

		if strings.TrimSpace(cred.ID) == "" {
			cred.ID = uuid.New().String()
		}
		cred.ProviderID = strings.TrimSpace(providerCfg.Id)
		cred.ProviderType = strings.TrimSpace(providerCfg.ProviderType)
		if cred.Metadata == nil {
			cred.Metadata = make(map[string]any)
		}
		cred.Metadata[credentialmgr.MetadataRefreshNameKey] = strings.ToLower(strings.TrimSpace(authenticator))
		if strings.TrimSpace(loginScope) != "" {
			cred.Scope = strings.TrimSpace(loginScope)
		} else {
			cred.Scope = credentialmgr.ProviderIDCredentialScope(cred.ProviderID)
		}
		if strings.TrimSpace(loginLabel) != "" {
			cred.Label = strings.TrimSpace(loginLabel)
		}

		if err := refresher.RegisterLoginCredential(ctx, cliauth.NewCLIAuthCredential(cred)); err != nil {
			return err
		}

		saved, err := credMgr.GetCredentialWithError(cred.ID)
		if err != nil {
			return err
		}
		if saved == nil {
			return fmt.Errorf("credential saved but could not be reloaded")
		}

		if outputFormat == "json" {
			return printJSON(saved)
		}
		printCliauthCredentialTable([]*credentialmgr.Credential{saved})
		fmt.Fprintf(os.Stderr, "saved to gateway %s\n", globalGatewayAddr)
		return nil
	},
}

// ── table formatter ───────────────────────────────────────────────────────────

func printCliauthCredentialTable(items []*credentialmgr.Credential) {
	headers := []string{"ID", "PROVIDER-TYPE", "PROVIDER-ID", "TYPE", "LABEL", "DISABLED"}
	rows := make([][]string, 0, len(items))
	for _, c := range items {
		rows = append(rows, []string{
			dash(c.ID),
			dash(c.ProviderType),
			dash(c.ProviderID),
			dash(c.Type),
			dash(c.Label),
			boolStr(c.Disabled),
		})
	}
	printTable(headers, rows)
}

// ── status reporter ───────────────────────────────────────────────────────────

type cliauthStatusReporter struct {
	lastKey string
}

func (r *cliauthStatusReporter) UpdateLoginStatus(update cliauth.LoginStatusUpdate) {
	key := strings.Join([]string{
		strings.TrimSpace(update.Phase),
		strings.TrimSpace(update.Message),
		strings.TrimSpace(update.VerificationURL),
		strings.TrimSpace(update.UserCode),
	}, "|")
	if key == r.lastKey {
		return
	}
	r.lastKey = key

	if update.Phase != "" {
		fmt.Fprintf(os.Stderr, "[%s] ", update.Phase)
	}
	if update.Message != "" {
		fmt.Fprint(os.Stderr, update.Message)
	}
	if renderedURL := renderLoginStatusURL(update); renderedURL != "" {
		fmt.Fprintf(os.Stderr, " %s", renderedURL)
	}
	if update.UserCode != "" {
		fmt.Fprintf(os.Stderr, " code=%s", update.UserCode)
	}
	fmt.Fprintln(os.Stderr)
}

func renderLoginStatusURL(update cliauth.LoginStatusUpdate) string {
	rawURL := strings.TrimSpace(update.VerificationURL)
	if rawURL == "" {
		return ""
	}

	switch strings.TrimSpace(update.Phase) {
	case "waiting_for_callback":
		return ""
	case "waiting_for_manual_callback":
		return "(manual verification URL: " + rawURL + ")"
	default:
		return rawURL
	}
}

func validateLoginAuthenticator(cmd *cobra.Command, raw string) (string, error) {
	authenticator := strings.TrimSpace(raw)
	supported := cliauth.ListAuthenticatorTypes()
	if authenticator == "" {
		return "", fmt.Errorf("--authenticator is required\nsupported authenticators: %s\n\n%s", strings.Join(supported, ", "), cmd.UsageString())
	}
	if !slices.Contains(supported, authenticator) {
		return "", fmt.Errorf("unsupported --authenticator %q\nsupported authenticators: %s\n\n%s", authenticator, strings.Join(supported, ", "), cmd.UsageString())
	}
	return authenticator, nil
}

// ── init ──────────────────────────────────────────────────────────────────────

func init() {
	cliauthCmd.PersistentFlags().StringVar(&globalGatewayAddr, "agw-admin-addr", envOr("AGW_ADMIN_ADDR", cliauthstore.DefaultGatewayAddr()), "agent-gateway admin API address")
	cliauthCmd.PersistentFlags().StringVar(&gwUser, "agw-admin-user", envOr("AGW_ADMIN_USER", ""), "gateway admin username")
	cliauthCmd.PersistentFlags().StringVar(&gwPassword, "agw-admin-password", envOr("AGW_ADMIN_PASSWORD", ""), "gateway admin password")

	cliauthLoginCmd.Flags().StringVar(&loginAuthenticator, "authenticator", "", "authenticator type: claudecode, codex, gemini (required)")
	cliauthLoginCmd.Flags().StringVar(&loginProviderID, "provider-id", "", "provider ID to bind the credential to")
	cliauthLoginCmd.Flags().StringVar(&loginScope, "scope", "", "credential scope override (defaults to provider-id scope)")
	cliauthLoginCmd.Flags().StringVar(&loginLabel, "label", "", "credential label")
	cliauthLoginCmd.Flags().IntVar(&loginCallbackPort, "callback-port", 0, "local OAuth callback port override")
	cliauthLoginCmd.Flags().BoolVar(&loginNoBrowser, "no-browser", false, "print the login URL instead of opening a browser")
	cliauthLoginCmd.Flags().BoolVar(&loginDeviceFlow, "device-flow", false, "use device flow when supported (Codex only)")
	cliauthLoginCmd.Flags().StringVar(&loginTransportProfile, "transport-profile", "", "transport profile override (for example: browser_like_tls)")

	cliauthCmd.AddCommand(
		cliauthLoginCmd,
	)
	rootCmd.AddCommand(cliauthCmd)
}
