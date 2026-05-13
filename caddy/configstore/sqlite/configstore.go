package sqlite

import (
	"path/filepath"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	configstoresqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
)

type SQLiteConfigStoreBackend struct {
	SQLitePath string `json:"sqlite_path,omitempty"`

	backend configstore.ConfigStoreBackend
}

func init() {
	caddy.RegisterModule(SQLiteConfigStoreBackend{})
}

func (SQLiteConfigStoreBackend) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "agent_gateway.config_store_backends.sqlite",
		New: func() caddy.Module { return new(SQLiteConfigStoreBackend) },
	}
}

func (s *SQLiteConfigStoreBackend) Provision(ctx caddy.Context) error {
	dbPath := s.SQLitePath
	if dbPath == "" {
		dbPath = filepath.Join(caddy.AppDataDir(), "agent-gateway", "configstore.db")
		s.SQLitePath = dbPath
	}

	backend, err := configstore.OpenBackend(ctx, "sqlite", configstoresqlite.Config{
		SQLitePath: dbPath,
	}, ctx.Logger(s))
	if err != nil {
		return err
	}
	s.backend = backend
	return nil
}

func (s *SQLiteConfigStoreBackend) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
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

func (s *SQLiteConfigStoreBackend) Register(name string, storeSchema configstore.StoreSchema) error {
	return s.backend.Register(name, storeSchema)
}

func (s *SQLiteConfigStoreBackend) Get(name string) (configstore.ConfigStore, error) {
	return s.backend.Get(name)
}

var (
	_ caddy.Module                   = (*SQLiteConfigStoreBackend)(nil)
	_ caddy.Provisioner              = (*SQLiteConfigStoreBackend)(nil)
	_ caddyfile.Unmarshaler          = (*SQLiteConfigStoreBackend)(nil)
	_ configstore.ConfigStoreBackend = (*SQLiteConfigStoreBackend)(nil)
)
