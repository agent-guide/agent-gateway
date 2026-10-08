// Package caddycmd adapts the upstream Caddy process entry. The designated
// reusable surface, protected when released with its consumer contracts, is:
// Command, New, (*Command).Assembly and (*Command).Run. Run is process-scoped;
// Caddy owns os.Args, signals, output and os.Exit, exactly as in Community main.
package caddycmd

import (
	"encoding/json"
	"fmt"
	"github.com/agent-guide/agent-gateway/internal/distributionruntime"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	upstream "github.com/caddyserver/caddy/v2/cmd"
	"github.com/spf13/cobra"
	"os"
)

// Command holds a distribution snapshot. Standard modules remain main imports.
type Command struct{ assembly distribution.Assembly }

// New constructs a fresh metadata snapshot without executing Caddy.
func New(a distribution.Assembly) *Command { return &Command{assembly: a.Clone()} }

// Assembly returns a defensive copy of the explicitly linked metadata.
func (c *Command) Assembly() distribution.Assembly { return c.assembly.Clone() }

// Run delegates command construction, lifecycle and exit codes to upstream.
func (c *Command) Run() {
	if err := distributionruntime.Install(c.assembly); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	upstream.Main()
}

// Manifest validates and returns canonical, independent metadata.
func (c *Command) Manifest() (distribution.Assembly, error) { return c.assembly.Manifest() }
func init() {
	upstream.RegisterCommand(upstream.Command{Name: "manifest", Short: "Show linked Agent Gateway capabilities", CobraFunc: func(cmd *cobra.Command) {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			manifest, err := distributionruntime.Current().Manifest()
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(manifest)
		}
	}})
}
