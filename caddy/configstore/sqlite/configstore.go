package sqlite

import (
	"context"
	"path/filepath"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"

	"github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	configstoresqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
)

type SQLiteConfigStore struct {
	SQLitePath string `json:"sqlite_path,omitempty"`

	store *configstoresqlite.SQLiteConfigStore
}

func init() {
	caddy.RegisterModule(SQLiteConfigStore{})
}

func (SQLiteConfigStore) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "agent_gateway.config_stores.sqlite",
		New: func() caddy.Module { return new(SQLiteConfigStore) },
	}
}

func (s *SQLiteConfigStore) Provision(ctx caddy.Context) error {
	dbPath := s.SQLitePath
	if dbPath == "" {
		dbPath = filepath.Join(caddy.AppDataDir(), "agent-gateway", "configstore.db")
		s.SQLitePath = dbPath
	}

	store, err := configstoresqlite.Open(ctx, configstoresqlite.Config{
		SQLitePath: dbPath,
	}, ctx.Logger(s))
	if err != nil {
		return err
	}
	s.store = store
	return nil
}

func (s *SQLiteConfigStore) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "path":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.SQLitePath = d.Val()
			default:
				return d.Errf("unknown sqlite config_store subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

func (s *SQLiteConfigStore) GetCredentialStore(ctx context.Context, decodeCredential intf.ConfigObjectDecoder) (intf.CredentialStorer, error) {
	return s.store.GetCredentialStore(ctx, decodeCredential)
}

func (s *SQLiteConfigStore) GetProviderConfigStore(ctx context.Context, decodeProviderConfig intf.ConfigObjectDecoder) (intf.ProviderConfigStorer, error) {
	return s.store.GetProviderConfigStore(ctx, decodeProviderConfig)
}

func (s *SQLiteConfigStore) GetVirtualKeyStore(ctx context.Context, decodeVirtualKey intf.ConfigObjectDecoder) (intf.VirtualKeyStorer, error) {
	return s.store.GetVirtualKeyStore(ctx, decodeVirtualKey)
}

func (s *SQLiteConfigStore) GetRouteStore(ctx context.Context, decodeRoute intf.ConfigObjectDecoder) (intf.RouteStorer, error) {
	return s.store.GetRouteStore(ctx, decodeRoute)
}

func (s *SQLiteConfigStore) GetModelStore(ctx context.Context, decodeModel intf.ConfigObjectDecoder) (intf.ModelStorer, error) {
	return s.store.GetModelStore(ctx, decodeModel)
}

var (
	_ caddy.Module          = (*SQLiteConfigStore)(nil)
	_ caddy.Provisioner     = (*SQLiteConfigStore)(nil)
	_ caddyfile.Unmarshaler = (*SQLiteConfigStore)(nil)
	_ intf.ConfigStorer     = (*SQLiteConfigStore)(nil)
)
