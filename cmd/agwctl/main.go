package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	if err := loadRuntimeEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

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
