package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agent-guide/caddy-agent-gateway/pkg/configstore/intf"
)

type SQLiteConfigStore struct {
	SQLitePath string `json:"sqlite_path,omitempty"`

	logger          *zap.Logger
	db              *gorm.DB
	credentialStore *CredentialStore
	providerStore   *ProviderConfigStore
	virtualKeyStore *VirtualKeyStore
	routeStore      *RouteStore
	modelStore      *ModelStore
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
	s.logger = ctx.Logger(s)

	dbPath := s.SQLitePath
	if dbPath == "" {
		dbPath = filepath.Join(caddy.AppDataDir(), "caddy-agent-gateway", "configstore.db")
		s.SQLitePath = dbPath
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return err
		}
		// Create DB file
		f, err := os.Create(dbPath)
		if err != nil {
			return err
		}
		_ = f.Close()
	}
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_synchronous=NORMAL&_cache_size=10000&_busy_timeout=60000&_wal_autocheckpoint=1000&_foreign_keys=1", dbPath)
	s.logger.Debug("opening DB with dsn", zap.String("dsn", dsn))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: newGormLogger(*s.logger),
	})
	if err != nil {
		return err
	}
	s.logger.Debug("sqlite db opened for SqliteStore")

	s.db = db

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
	if s.credentialStore != nil {
		return s.credentialStore, nil
	}

	credentialStore, err := NewCredentialStore(ctx, s.db, decodeCredential)
	if err != nil {
		return nil, fmt.Errorf("init credential store: %w", err)
	}
	s.credentialStore = credentialStore
	return credentialStore, nil
}

func (s *SQLiteConfigStore) GetProviderConfigStore(ctx context.Context, decodeProviderConfig intf.ConfigObjectDecoder) (intf.ProviderConfigStorer, error) {
	providerStore, err := NewProviderConfigStore(ctx, s.db, decodeProviderConfig)
	if err != nil {
		return nil, fmt.Errorf("init provider config store: %w", err)
	}
	s.providerStore = providerStore
	return s.providerStore, nil
}

func (s *SQLiteConfigStore) GetVirtualKeyStore(ctx context.Context, decodeVirtualKey intf.ConfigObjectDecoder) (intf.VirtualKeyStorer, error) {
	if s.virtualKeyStore != nil {
		return s.virtualKeyStore, nil
	}

	virtualKeyStore, err := NewVirtualKeyStore(ctx, s.db, decodeVirtualKey)
	if err != nil {
		return nil, fmt.Errorf("init virtual key store: %w", err)
	}
	s.virtualKeyStore = virtualKeyStore
	return virtualKeyStore, nil
}

func (s *SQLiteConfigStore) GetRouteStore(ctx context.Context, decodeRoute intf.ConfigObjectDecoder) (intf.RouteStorer, error) {
	if s.routeStore != nil {
		return s.routeStore, nil
	}

	routeStore, err := NewRouteStore(ctx, s.db, decodeRoute)
	if err != nil {
		return nil, fmt.Errorf("init route store: %w", err)
	}
	s.routeStore = routeStore
	return routeStore, nil
}

func (s *SQLiteConfigStore) GetModelStore(ctx context.Context, decodeModel intf.ConfigObjectDecoder) (intf.ModelStorer, error) {
	if s.modelStore != nil {
		return s.modelStore, nil
	}

	modelStore, err := NewModelStore(ctx, s.db, decodeModel)
	if err != nil {
		return nil, fmt.Errorf("init model store: %w", err)
	}
	s.modelStore = modelStore
	return modelStore, nil
}

var (
	_ caddy.Module          = (*SQLiteConfigStore)(nil)
	_ caddy.Provisioner     = (*SQLiteConfigStore)(nil)
	_ caddyfile.Unmarshaler = (*SQLiteConfigStore)(nil)
	_ intf.ConfigStorer     = (*SQLiteConfigStore)(nil)
)
