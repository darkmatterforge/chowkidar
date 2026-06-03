package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AuthConfig is persisted in auth.yaml and controls the login requirement.
type AuthConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"` // bcrypt hash
}

// LoadAuthConfig reads auth.yaml. On first boot (file absent), it returns a
// config with Enabled=true and empty credentials, forcing the setup flow.
func LoadAuthConfig(configDir string) (*AuthConfig, error) {
	path := filepath.Join(configDir, "auth.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// First boot: auth enabled but no credentials set — forces setup on first visit.
		return &AuthConfig{Enabled: true, Username: "", PasswordHash: ""}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg AuthConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveAuthConfig writes cfg to auth.yaml with mode 0600 (owner-read only).
func SaveAuthConfig(configDir string, cfg *AuthConfig) error {
	path := filepath.Join(configDir, "auth.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
