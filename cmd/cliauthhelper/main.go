package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/agent-guide/caddy-agent-gateway/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/google/uuid"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "CLIAuthHelper: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}

	switch args[0] {
	case "authenticators":
		return runAuthenticators(args[1:])
	case "login":
		return runLogin(args[1:])
	case "list":
		return runList(args[1:])
	case "get":
		return runGet(args[1:])
	case "delete":
		return runDelete(args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runAuthenticators(args []string) error {
	fs := flag.NewFlagSet("authenticators", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, name := range cliauth.ListAuthenticatorTypes() {
		fmt.Println(name)
	}
	return nil
}

func runLogin(args []string) error {
	defaultStore, err := defaultStorePath()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	authName := fs.String("authenticator", "", "Authenticator name: codex, claude, gemini")
	storePath := fs.String("store", defaultStore, "Credential JSON file path")
	providerID := fs.String("provider-id", "", "Provider ID override, defaults to provider type")
	label := fs.String("label", "", "Credential label override")
	callbackPort := fs.Int("callback-port", 0, "Local callback port override")
	noBrowser := fs.Bool("no-browser", false, "Print the login URL instead of opening a browser")
	useDeviceFlow := fs.Bool("device-flow", false, "Use device flow when supported (Codex only)")
	jsonOutput := fs.Bool("json", false, "Print the saved credential as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*authName) == "" {
		return fmt.Errorf("--authenticator is required")
	}

	refresher, credMgr, err := newManagers(*storePath)
	if err != nil {
		return err
	}

	auth, err := cliauth.NewAuthenticator(*authName)
	if err != nil {
		return err
	}
	if err := configureAuthenticator(auth, cliauth.AuthenticatorConfig{
		CallbackPort: *callbackPort,
		NoBrowser:    *noBrowser,
		DeviceFlow:   *useDeviceFlow,
	}); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	reporter := &statusReporter{}
	cred, err := auth.Login(ctx, reporter)
	if err != nil {
		return err
	}

	if strings.TrimSpace(*providerID) != "" {
		cred.ProviderID = strings.TrimSpace(*providerID)
	}
	if strings.TrimSpace(cred.ID) == "" {
		cred.ID = uuid.New().String()
	}
	if strings.TrimSpace(cred.ProviderID) == "" {
		cred.ProviderID = strings.TrimSpace(cred.ProviderType)
	}
	if strings.TrimSpace(*label) != "" {
		cred.Label = strings.TrimSpace(*label)
	}

	if err := refresher.RegisterLoginCredential(ctx, cliauth.NewCLIAuthCredential(cred)); err != nil {
		return err
	}

	saved := credMgr.GetCredential(cred.ID)
	if saved == nil {
		return fmt.Errorf("credential saved but could not be reloaded")
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(saved)
	}

	fmt.Printf("saved credential %s for %s/%s in %s\n", saved.ID, saved.ProviderType, saved.ProviderID, *storePath)
	return nil
}

func runList(args []string) error {
	defaultStore, err := defaultStorePath()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	storePath := fs.String("store", defaultStore, "Credential JSON file path")
	source := fs.String("source", "", "Filter by source")
	providerType := fs.String("provider-type", "", "Filter by provider type")
	providerID := fs.String("provider-id", "", "Filter by provider ID")
	jsonOutput := fs.Bool("json", false, "Print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	credMgr, err := newCredentialManager(*storePath)
	if err != nil {
		return err
	}
	items := credMgr.ListCredentials(credentialmgr.Filter{
		Source:       *source,
		ProviderType: *providerType,
		ProviderID:   *providerID,
	})

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}

	if len(items) == 0 {
		fmt.Println("no credentials found")
		return nil
	}
	for _, item := range items {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", item.ID, item.ProviderType, item.ProviderID, item.Source, item.Label)
	}
	return nil
}

func runGet(args []string) error {
	defaultStore, err := defaultStorePath()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	storePath := fs.String("store", defaultStore, "Credential JSON file path")
	id := fs.String("id", "", "Credential ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("--id is required")
	}

	credMgr, err := newCredentialManager(*storePath)
	if err != nil {
		return err
	}
	cred := credMgr.GetCredential(*id)
	if cred == nil {
		return fmt.Errorf("credential %q not found", *id)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(cred)
}

func runDelete(args []string) error {
	defaultStore, err := defaultStorePath()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	storePath := fs.String("store", defaultStore, "Credential JSON file path")
	id := fs.String("id", "", "Credential ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("--id is required")
	}

	credMgr, err := newCredentialManager(*storePath)
	if err != nil {
		return err
	}
	if err := credMgr.DeregisterCredential(context.Background(), *id); err != nil {
		return err
	}
	fmt.Printf("deleted credential %s\n", strings.TrimSpace(*id))
	return nil
}

func configureAuthenticator(auth cliauth.Authenticator, overrides cliauth.AuthenticatorConfig) error {
	return cliauth.ApplyAuthenticatorConfigOverrides(auth, overrides)
}

func newManagers(storePath string) (*cliauth.AutoRefresher, *CredentialManager, error) {
	credMgr, err := newCredentialManager(storePath)
	if err != nil {
		return nil, nil, err
	}
	refresher := cliauth.NewAutoRefresher(credMgr, nil)
	if err := refresher.Load(context.Background()); err != nil {
		return nil, nil, err
	}
	return refresher, credMgr, nil
}

func newCredentialManager(storePath string) (*CredentialManager, error) {
	expanded, err := expandPath(storePath)
	if err != nil {
		return nil, err
	}
	return NewCredentialManager(expanded)
}

type statusReporter struct {
	lastKey string
}

func (r *statusReporter) UpdateLoginStatus(update cliauth.LoginStatusUpdate) {
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

func defaultStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".cliauthhelper", "credentials.json"), nil
}

func expandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		path = filepath.Join(home, path[2:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}
	return abs, nil
}

func printUsage(w *os.File) {
	_, _ = fmt.Fprintln(w, "Usage: cliauthhelper <command> [flags]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Commands:")
	_, _ = fmt.Fprintln(w, "  authenticators          List supported CLI authenticators")
	_, _ = fmt.Fprintln(w, "  login                   Run an interactive CLIAuth login flow")
	_, _ = fmt.Fprintln(w, "  list                    List stored credentials")
	_, _ = fmt.Fprintln(w, "  get --id <id>           Show one stored credential")
	_, _ = fmt.Fprintln(w, "  delete --id <id>        Delete one stored credential")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Examples:")
	_, _ = fmt.Fprintln(w, "  cliauthhelper authenticators")
	_, _ = fmt.Fprintln(w, "  cliauthhelper login --authenticator codex --device-flow --json")
	_, _ = fmt.Fprintln(w, "  cliauthhelper list --json")
}
