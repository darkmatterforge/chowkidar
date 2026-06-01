package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type AuthConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"` // bcrypt hash
}

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

func SaveAuthConfig(configDir string, cfg *AuthConfig) error {
	path := filepath.Join(configDir, "auth.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
