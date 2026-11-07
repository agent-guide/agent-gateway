package sqlite

import (
	"context"
	"path/filepath"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	configstoresqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"gorm.io/gorm"
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

// The app owns shutdown order; a child Caddy Cleanup must not close a connection
// while the app is still draining its usage sink.
func (s *SQLiteConfigStoreBackend) Close() error {
	if closer, ok := s.backend.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// Transaction preserves the optional backend capability through the Caddy
// module boundary, including the same schema-bound stores and rollback rules.
func (s *SQLiteConfigStoreBackend) Transaction(ctx context.Context, names []string, apply func(map[string]configstore.ConfigStore) error) error {
	backend, ok := s.backend.(configstore.TransactionalBackend)
	if !ok {
		return configstore.ErrTransactionsUnsupported
	}
	return backend.Transaction(ctx, names, apply)
}

func (s *SQLiteConfigStoreBackend) Database() *gorm.DB {
	provider, ok := s.backend.(interface{ Database() *gorm.DB })
	if !ok {
		return nil
	}
	return provider.Database()
}

var (
	_ caddy.Module                     = (*SQLiteConfigStoreBackend)(nil)
	_ caddy.Provisioner                = (*SQLiteConfigStoreBackend)(nil)
	_ caddyfile.Unmarshaler            = (*SQLiteConfigStoreBackend)(nil)
	_ configstore.ConfigStoreBackend   = (*SQLiteConfigStoreBackend)(nil)
	_ interface{ Database() *gorm.DB } = (*SQLiteConfigStoreBackend)(nil)
)
