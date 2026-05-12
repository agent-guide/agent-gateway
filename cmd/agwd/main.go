package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agent-guide/agent-gateway/standalone/server"
	"github.com/spf13/cobra"

	// CLI authenticators register runtime factories through init.
	_ "github.com/agent-guide/agent-gateway/pkg/cliauth/authenticator"

	// LLM providers register runtime factories through init.
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/anthropic"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/deepseek"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/gemini"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/ollama"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openrouter"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/zhipu"
)

func main() {
	var opts server.Options
	rootCmd := &cobra.Command{
		Use:   "agwd",
		Short: "Standalone Agent Gateway daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return server.Run(ctx, opts)
		},
	}
	rootCmd.Flags().StringVar(&opts.Addr, "addr", "127.0.0.1:8080", "LLM gateway listen address")
	rootCmd.Flags().StringVar(&opts.AdminAddr, "admin-addr", "127.0.0.1:8081", "Admin API listen address")
	rootCmd.Flags().StringVar(&opts.AdminUser, "admin-user", os.Getenv("AGW_ADMIN_USER"), "admin username")
	rootCmd.Flags().StringVar(&opts.AdminPasswordHash, "admin-password-hash", os.Getenv("AGW_ADMIN_PASSWORD_HASH"), "bcrypt hash of admin password")
	rootCmd.Flags().StringVar(&opts.ConfigStorePath, "config-store", "./data/configstore.db", "SQLite config store file")
	rootCmd.Flags().StringVar(&opts.StaticConfigPath, "static-config", "", "gateway bundle YAML file loaded as read-only static configuration")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
