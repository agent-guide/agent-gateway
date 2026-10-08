package ctlcmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
