package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/cliauthstore"
	"github.com/agent-guide/caddy-agent-gateway/pkg/cliauth"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/cliauth/authenticator"
	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var cliauthStorePath string

// ── cliauth ───────────────────────────────────────────────────────────────────

var cliauthCmd = &cobra.Command{
	Use:   "cliauth",
	Short: "Manage local CLI auth credentials on the agwctl machine",
}

// ── cliauth authenticators ────────────────────────────────────────────────────

var cliauthAuthenticatorsCmd = &cobra.Command{
	Use:   "authenticators",
	Short: "List supported CLI authenticator types",
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, name := range cliauth.ListAuthenticatorTypes() {
			fmt.Println(name)
		}
		return nil
	},
}

// ── cliauth login ─────────────────────────────────────────────────────────────

var (
	loginAuthenticator string
	loginProviderID    string
	loginLabel         string
	loginCallbackPort  int
	loginNoBrowser     bool
	loginDeviceFlow    bool
)

var cliauthLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Run an interactive CLI auth login flow and save the credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(loginAuthenticator) == "" {
			return fmt.Errorf("--authenticator is required")
		}

		credMgr, err := cliauthstore.NewFromPath(cliauthStorePath)
		if err != nil {
			return err
		}
		refresher := cliauth.NewAutoRefresher(credMgr, nil)
		if err := refresher.Load(context.Background()); err != nil {
			return err
		}

		auth, err := cliauth.NewAuthenticator(loginAuthenticator)
		if err != nil {
			return err
		}
		if err := cliauth.ApplyAuthenticatorConfigOverrides(auth, cliauth.AuthenticatorConfig{
			CallbackPort: loginCallbackPort,
			NoBrowser:    loginNoBrowser,
			DeviceFlow:   loginDeviceFlow,
		}); err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		reporter := &cliauthStatusReporter{}
		cred, err := auth.Login(ctx, reporter)
		if err != nil {
			return err
		}

		if strings.TrimSpace(loginProviderID) != "" {
			cred.ProviderID = strings.TrimSpace(loginProviderID)
		}
		if strings.TrimSpace(cred.ID) == "" {
			cred.ID = uuid.New().String()
		}
		if strings.TrimSpace(cred.ProviderID) == "" {
			cred.ProviderID = strings.TrimSpace(cred.ProviderType)
		}
		if strings.TrimSpace(loginLabel) != "" {
			cred.Label = strings.TrimSpace(loginLabel)
		}

		if err := refresher.RegisterLoginCredential(ctx, cliauth.NewCLIAuthCredential(cred)); err != nil {
			return err
		}

		saved := credMgr.GetCredential(cred.ID)
		if saved == nil {
			return fmt.Errorf("credential saved but could not be reloaded")
		}

		if outputFormat == "json" {
			return printJSON(saved)
		}
		printCliauthCredentialTable([]*credentialmgr.Credential{saved})
		fmt.Fprintf(os.Stderr, "saved to %s\n", cliauthStorePath)
		return nil
	},
}

// ── cliauth list ──────────────────────────────────────────────────────────────

var (
	listSource       string
	listProviderType string
	listProviderID   string
)

var cliauthListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored CLI auth credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credMgr, err := cliauthstore.NewFromPath(cliauthStorePath)
		if err != nil {
			return err
		}
		items := credMgr.ListCredentials(credentialmgr.Filter{
			Source:       listSource,
			ProviderType: listProviderType,
			ProviderID:   listProviderID,
		})

		if outputFormat == "json" {
			return printJSON(items)
		}
		if len(items) == 0 {
			fmt.Println("no credentials found")
			return nil
		}
		printCliauthCredentialTable(items)
		return nil
	},
}

// ── cliauth get ───────────────────────────────────────────────────────────────

var cliauthGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show one stored credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credMgr, err := cliauthstore.NewFromPath(cliauthStorePath)
		if err != nil {
			return err
		}
		cred := credMgr.GetCredential(args[0])
		if cred == nil {
			return fmt.Errorf("credential %q not found", args[0])
		}
		return printJSON(cred)
	},
}

// ── cliauth delete ────────────────────────────────────────────────────────────

var cliauthDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete one stored credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credMgr, err := cliauthstore.NewFromPath(cliauthStorePath)
		if err != nil {
			return err
		}
		if err := credMgr.DeregisterCredential(context.Background(), args[0]); err != nil {
			return err
		}
		fmt.Printf("deleted credential %s\n", args[0])
		return nil
	},
}

// ── table formatter ───────────────────────────────────────────────────────────

func printCliauthCredentialTable(items []*credentialmgr.Credential) {
	headers := []string{"ID", "TYPE", "PROVIDER-ID", "SOURCE", "LABEL", "DISABLED"}
	rows := make([][]string, 0, len(items))
	for _, c := range items {
		rows = append(rows, []string{
			dash(c.ID),
			dash(c.ProviderType),
			dash(c.ProviderID),
			dash(c.Source),
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
	if update.VerificationURL != "" {
		fmt.Fprintf(os.Stderr, " %s", update.VerificationURL)
	}
	if update.UserCode != "" {
		fmt.Fprintf(os.Stderr, " code=%s", update.UserCode)
	}
	fmt.Fprintln(os.Stderr)
}

// ── init ──────────────────────────────────────────────────────────────────────

func init() {
	defaultStore, _ := cliauthstore.DefaultStorePath()

	cliauthCmd.PersistentFlags().StringVar(&cliauthStorePath, "store", defaultStore, "credential JSON file path")

	cliauthLoginCmd.Flags().StringVar(&loginAuthenticator, "authenticator", "", "authenticator type: codex, claude, gemini (required)")
	cliauthLoginCmd.Flags().StringVar(&loginProviderID, "provider-id", "", "provider ID override (defaults to provider type)")
	cliauthLoginCmd.Flags().StringVar(&loginLabel, "label", "", "credential label")
	cliauthLoginCmd.Flags().IntVar(&loginCallbackPort, "callback-port", 0, "local OAuth callback port override")
	cliauthLoginCmd.Flags().BoolVar(&loginNoBrowser, "no-browser", false, "print the login URL instead of opening a browser")
	cliauthLoginCmd.Flags().BoolVar(&loginDeviceFlow, "device-flow", false, "use device flow when supported (Codex only)")

	cliauthListCmd.Flags().StringVar(&listSource, "source", "", "filter by source")
	cliauthListCmd.Flags().StringVar(&listProviderType, "provider-type", "", "filter by provider type")
	cliauthListCmd.Flags().StringVar(&listProviderID, "provider-id", "", "filter by provider ID")

	cliauthCmd.AddCommand(
		cliauthAuthenticatorsCmd,
		cliauthLoginCmd,
		cliauthListCmd,
		cliauthGetCmd,
		cliauthDeleteCmd,
	)
	rootCmd.AddCommand(cliauthCmd)
}
