package cliauth

// ApplyAuthenticatorConfigOverrides reads the authenticator's current runtime
// config, merges the provided overrides, and reapplies the result.
func ApplyAuthenticatorConfigOverrides(auth Authenticator, overrides AuthenticatorConfig) error {
	cfg := auth.GetConfig()
	cfg.ApplyOverrides(overrides)
	return auth.SetConfig(cfg)
}
