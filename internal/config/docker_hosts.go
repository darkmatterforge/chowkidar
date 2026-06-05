package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"chowkidar/internal/crypto"

	"gopkg.in/yaml.v3"
)

type DockerHostProfile struct {
	ID            string `yaml:"id"                        json:"id"`
	Name          string `yaml:"name"                      json:"name"`
	Type          string `yaml:"type"                      json:"type"`
	Endpoint      string `yaml:"endpoint"                  json:"endpoint"`
	Enabled       bool   `yaml:"enabled"                   json:"enabled"`
	TLSCACert     string `yaml:"tls_ca_cert,omitempty"     json:"tls_ca_cert,omitempty"`
	TLSCert       string `yaml:"tls_cert,omitempty"        json:"tls_cert,omitempty"`
	TLSKey        string `yaml:"tls_key,omitempty"         json:"tls_key,omitempty"`
	TLSSkipVerify bool     `yaml:"tls_skip_verify,omitempty" json:"tls_skip_verify,omitempty"`
	Notifications          []string `yaml:"notifications,omitempty"          json:"notifications,omitempty"`
	DownTemplate           string `yaml:"downTemplate,omitempty"      json:"downTemplate,omitempty"`
	RecoveryTemplate       string `yaml:"recoveryTemplate,omitempty"  json:"recoveryTemplate,omitempty"`
	MonitorIntervalSeconds int `yaml:"monitorIntervalSeconds,omitempty" json:"monitorIntervalSeconds,omitempty"`
	PingTimeoutSeconds     int `yaml:"pingTimeoutSeconds,omitempty"     json:"pingTimeoutSeconds,omitempty"`
	OfflineConfirmSeconds  int `yaml:"offlineConfirmSeconds,omitempty"  json:"offlineConfirmSeconds,omitempty"`
	BuiltIn                bool     `yaml:"-"                                json:"built_in,omitempty"`
}

type DockerHostsFile struct {
	Profiles []DockerHostProfile `yaml:"profiles" json:"profiles"`
}

func LoadDockerHostProfiles(configDir string) ([]DockerHostProfile, error) {
	filePath := filepath.Join(configDir, "docker_hosts.yaml")
	raw, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []DockerHostProfile{}, nil
		}
		return nil, fmt.Errorf("read docker hosts file: %w", err)
	}

	var out DockerHostsFile
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse docker hosts yaml: %w", err)
	}
	if out.Profiles == nil {
		out.Profiles = []DockerHostProfile{}
	}

	// Decrypt TLS fields in memory so callers always work with plaintext.
	// On key mismatch, clear the field so the app still starts; user must re-enter.
	for i, p := range out.Profiles {
		if dec, err := crypto.MaybeDecrypt(p.TLSCACert); err != nil {
			out.Profiles[i].TLSCACert = ""
		} else {
			out.Profiles[i].TLSCACert = dec
		}
		if dec, err := crypto.MaybeDecrypt(p.TLSCert); err != nil {
			out.Profiles[i].TLSCert = ""
		} else {
			out.Profiles[i].TLSCert = dec
		}
		if dec, err := crypto.MaybeDecrypt(p.TLSKey); err != nil {
			out.Profiles[i].TLSKey = ""
		} else {
			out.Profiles[i].TLSKey = dec
		}
	}

	return out.Profiles, nil
}

func normalizeDockerHostProfile(p DockerHostProfile, idx int) (DockerHostProfile, error) {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	p.Endpoint = strings.TrimSpace(p.Endpoint)
	if p.ID == "" {
		p.ID = fmt.Sprintf("docker-host-%d", idx+1)
	}
	if p.Name == "" {
		return DockerHostProfile{}, fmt.Errorf("docker host name is required")
	}
	if p.Type == "" {
		p.Type = "socket"
	}
	switch p.Type {
	case "socket", "http", "https":
	default:
		return DockerHostProfile{}, fmt.Errorf("unsupported docker host type %q", p.Type)
	}
	if p.Endpoint == "" {
		return DockerHostProfile{}, fmt.Errorf("docker host endpoint is required")
	}
	if p.Type == "socket" && !strings.HasPrefix(p.Endpoint, "/") {
		return DockerHostProfile{}, fmt.Errorf("socket docker host endpoint must be an absolute path")
	}
	if p.Type != "socket" && !strings.HasPrefix(p.Endpoint, p.Type+"://") {
		return DockerHostProfile{}, fmt.Errorf("docker host endpoint must start with %s://", p.Type)
	}
	if (p.TLSCert != "" && p.TLSKey == "") || (p.TLSKey != "" && p.TLSCert == "") {
		return DockerHostProfile{}, fmt.Errorf("tls_cert and tls_key must both be provided together")
	}
	return p, nil
}

func SaveDockerHostProfiles(configDir string, profiles []DockerHostProfile) error {
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	normalized := make([]DockerHostProfile, 0, len(profiles))
	for i, p := range profiles {
		n, err := normalizeDockerHostProfile(p, i)
		if err != nil {
			return err
		}

		// Encrypt TLS fields before writing to disk.
		if n.TLSCACert, err = crypto.MaybeEncrypt(n.TLSCACert); err != nil {
			return fmt.Errorf("encrypt tls_ca_cert for host %q: %w", n.ID, err)
		}
		if n.TLSCert, err = crypto.MaybeEncrypt(n.TLSCert); err != nil {
			return fmt.Errorf("encrypt tls_cert for host %q: %w", n.ID, err)
		}
		if n.TLSKey, err = crypto.MaybeEncrypt(n.TLSKey); err != nil {
			return fmt.Errorf("encrypt tls_key for host %q: %w", n.ID, err)
		}

		normalized = append(normalized, n)
	}

	payload := DockerHostsFile{Profiles: normalized}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal docker hosts yaml: %w", err)
	}

	filePath := filepath.Join(configDir, "docker_hosts.yaml")
	if err := os.WriteFile(filePath, raw, 0o600); err != nil {
		return fmt.Errorf("write docker hosts yaml: %w", err)
	}
	return nil
}
