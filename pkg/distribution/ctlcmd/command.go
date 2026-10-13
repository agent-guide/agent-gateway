// Package ctlcmd constructs management CLI commands without import-time process
// side effects. Options, NewCommand and Run are the designated reusable entry
// surface; release protection follows docs/design/enterprise-extension-contract.md.
package ctlcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/spf13/cobra"
)

// Options supplies distribution metadata; main owns linked validation factories.
type Options struct{ Assembly distribution.Assembly }

// NewCommand creates fresh commands, flags and output state. It reads existing
// environment defaults but does not load dotenv files or exit the process.
func NewCommand(opts Options) *cobra.Command {
	a := opts.Assembly.Clone()
	registry, assemblyErr := a.Registry()
	s := &commandState{extensionRegistry: registry}
	s.initRoot()
	s.rootCmd.PersistentPreRunE = func(*cobra.Command, []string) error { return assemblyErr }
	remote := false
	manifestCmd := &cobra.Command{Use: "manifest", Short: "Show linked validation capabilities", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if remote {
			m, err := s.newGatewayClient().Manifest(cmd.Context())
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(m)
		}
		m, err := a.Manifest()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(m)
	}}
	manifestCmd.Flags().BoolVar(&remote, "remote", false, "query the server runtime manifest")
	s.rootCmd.AddCommand(manifestCmd)
	s.initCaddy()
	s.initGateway()
	s.initACP()
	s.initAgent()
	s.initAgentRoute()
	s.initBuiltin()
	distribution.Annotate(s.rootCmd, opts.Assembly)
	return s.rootCmd
}

// Run loads .env.local then .env, executes a fresh command, and returns an exit
// code. It uses os.Args/stdout/stderr; only main calls os.Exit.
func Run(opts Options) int {
	if err := loadRuntimeEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cmd := NewCommand(opts)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
