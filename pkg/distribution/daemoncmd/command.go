// Package daemoncmd constructs standalone daemon commands. Options,
// ServerOptions, NewCommand and Run are the designated reusable entry surface;
// release protection follows docs/design/enterprise-extension-contract.md.
package daemoncmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/standalone/server"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// Options contains public server configuration and distribution metadata.
type Options struct {
	Assembly distribution.Assembly
	Server   ServerOptions
}

// ServerOptions contains only public configuration values.
type ServerOptions struct {
	Addr                     string
	AdminAddr                string
	ConfigStorePath          string
	StaticConfigPath         string
	ProviderTypes            []string
	CredentialRefreshCommand string
	CredentialRefreshArgs    []string
	AdminBasicAuthHash       string
	Metrics                  distribution.MetricsConfig
}

// NewCommand creates fresh flags without dotenv or signal side effects.
func NewCommand(assembly Options) *cobra.Command {
	assembly.Assembly = assembly.Assembly.Clone()
	registry, assemblyErr := assembly.Assembly.Registry()
	opts := assembly.Server
	opts.ProviderTypes = slices.Clone(opts.ProviderTypes)
	opts.CredentialRefreshArgs = slices.Clone(opts.CredentialRefreshArgs)
	opts.Metrics.OTLP.Headers = maps.Clone(opts.Metrics.OTLP.Headers)
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:8080"
	}
	if opts.AdminAddr == "" {
		opts.AdminAddr = "localhost:8019"
	}
	if opts.ConfigStorePath == "" {
		opts.ConfigStorePath = "./data/configstore.db"
	}
	if opts.AdminBasicAuthHash == "" {
		opts.AdminBasicAuthHash = os.Getenv("AGW_ADMIN_BASIC_AUTH_HASH")
	}
	if opts.Metrics.RetentionDays == 0 {
		opts.Metrics.RetentionDays = 30
	}
	if opts.Metrics.OTLP.Protocol == "" {
		opts.Metrics.OTLP.Protocol = "grpc"
	}
	rootCmd := &cobra.Command{
		Use:               "agwd",
		Short:             "Standalone Agent Gateway daemon",
		PersistentPreRunE: func(*cobra.Command, []string) error { return assemblyErr },
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return server.Run(ctx, server.Options{
				Registry: registry, Assembly: assembly.Assembly, Addr: opts.Addr, AdminAddr: opts.AdminAddr, ConfigStorePath: opts.ConfigStorePath, StaticConfigPath: opts.StaticConfigPath, ProviderTypes: opts.ProviderTypes, CredentialRefreshCommand: opts.CredentialRefreshCommand, CredentialRefreshArgs: opts.CredentialRefreshArgs, AdminBasicAuthHash: opts.AdminBasicAuthHash, Metrics: opts.Metrics,
			})
		},
	}
	rootCmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "LLM gateway listen address")
	rootCmd.Flags().StringVar(&opts.AdminAddr, "admin-addr", opts.AdminAddr, "Admin API listen address")
	rootCmd.Flags().StringVar(&opts.AdminBasicAuthHash, "admin-basic-auth-hash", opts.AdminBasicAuthHash, "admin Basic Auth configuration as username:bcrypt-hash")
	rootCmd.Flags().StringVar(&opts.ConfigStorePath, "config-store", opts.ConfigStorePath, "SQLite config store file")
	rootCmd.Flags().StringVar(&opts.StaticConfigPath, "static-config", opts.StaticConfigPath, "gateway bundle YAML file loaded as read-only static configuration")
	rootCmd.Flags().StringArrayVar(&opts.ProviderTypes, "provider-type", opts.ProviderTypes, "provider type enabled for this process; repeat to allow multiple types")
	rootCmd.Flags().StringVar(&opts.CredentialRefreshCommand, "credential-refresh-command", opts.CredentialRefreshCommand, "external credential refresh executable (default: agw-auth)")
	rootCmd.Flags().StringArrayVar(&opts.CredentialRefreshArgs, "credential-refresh-arg", opts.CredentialRefreshArgs, "static argument for the credential refresh executable; repeat as needed (default: refresh)")
	rootCmd.Flags().IntVar(&opts.Metrics.RetentionDays, "metrics-retention-days", opts.Metrics.RetentionDays, "retention window in days for persisted usage events")
	rootCmd.Flags().IntVar(&opts.Metrics.MaxAgentDepth, "max-agent-depth", opts.Metrics.MaxAgentDepth, "maximum inbound X-Agent-Depth allowed before rejecting a request; 0 disables enforcement")
	rootCmd.Flags().StringVar(&opts.Metrics.OTLP.Endpoint, "metrics-otlp-endpoint", opts.Metrics.OTLP.Endpoint, "OTLP collector endpoint (host:port or URL) for exporting usage events as spans; empty disables export")
	rootCmd.Flags().StringVar(&opts.Metrics.OTLP.Protocol, "metrics-otlp-protocol", opts.Metrics.OTLP.Protocol, "OTLP transport protocol: grpc or http")
	rootCmd.Flags().BoolVar(&opts.Metrics.OTLP.Insecure, "metrics-otlp-insecure", opts.Metrics.OTLP.Insecure, "disable TLS for the OTLP exporter")
	rootCmd.Flags().StringToStringVar(&opts.Metrics.OTLP.Headers, "metrics-otlp-header", opts.Metrics.OTLP.Headers, "header sent with every OTLP export request as name=value; repeat for multiple headers")
	rootCmd.Flags().StringVar(&opts.Metrics.OTLP.ServiceName, "metrics-otlp-service-name", opts.Metrics.OTLP.ServiceName, "OTel resource service.name for exported spans (default agent-gateway)")
	rootCmd.Flags().BoolVar(&opts.Metrics.OTLP.Components, "metrics-otlp-components", opts.Metrics.OTLP.Components, "additionally export one span per eino chat-model component call, nested under the interaction span")

	rootCmd.AddCommand(&cobra.Command{Use: "manifest", Short: "Show linked capabilities", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := assembly.Assembly.Manifest()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(m)
	}})
	distribution.Annotate(rootCmd, assembly.Assembly)
	return rootCmd
}

// Run loads dotenv, executes the daemon and returns the original process exit code.
func Run(opts Options) int {
	if err := loadRuntimeEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := NewCommand(opts).ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
func loadRuntimeEnv() error {
	for _, path := range []string{".env.local", ".env"} {
		if err := loadOptionalEnvFile(path); err != nil {
			return err
		}
	}
	return nil
}

func loadOptionalEnvFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := godotenv.Load(path); err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	return nil
}
