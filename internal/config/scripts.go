package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ScriptEntry struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Path    string `yaml:"path" json:"path"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
}

type ScriptsFile struct {
	Scripts []ScriptEntry `yaml:"scripts"`
}

func LoadScriptEntries(configDir string) ([]ScriptEntry, error) {
	filePath := filepath.Join(configDir, "scripts.yaml")
	raw, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []ScriptEntry{}, nil
		}
		return nil, fmt.Errorf("read scripts file: %w", err)
	}

	var out ScriptsFile
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse scripts yaml: %w", err)
	}
	if out.Scripts == nil {
		out.Scripts = []ScriptEntry{}
	}
	return out.Scripts, nil
}

func SaveScriptEntries(configDir string, scripts []ScriptEntry) error {
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	normalized := make([]ScriptEntry, 0, len(scripts))
	for i, s := range scripts {
		s.ID = strings.TrimSpace(s.ID)
		s.Name = strings.TrimSpace(s.Name)
		s.Path = strings.TrimSpace(s.Path)
		if s.ID == "" {
			s.ID = fmt.Sprintf("script-%d", i+1)
		}
		if s.Name == "" {
			return fmt.Errorf("script name is required")
		}
		if s.Path == "" {
			return fmt.Errorf("script path is required")
		}
		normalized = append(normalized, s)
	}

	payload := ScriptsFile{Scripts: normalized}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal scripts yaml: %w", err)
	}

	filePath := filepath.Join(configDir, "scripts.yaml")
	if err := os.WriteFile(filePath, raw, 0o600); err != nil {
		return fmt.Errorf("write scripts yaml: %w", err)
	}
	return nil
}
