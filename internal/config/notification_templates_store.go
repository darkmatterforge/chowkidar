package config

// notification_templates_store.go manages two files in the config directory:
//
//   notification_default_templates.yaml (embedded in binary)
//     Provider-level defaults bundled with the application.
//     To override, place a copy at /config/notification_default_templates.yaml.
//
//   notification_templates.yaml
//     Per-agent overrides, keyed by agent ID.
//     Takes precedence over notification_default_templates.yaml.
//
// Three-tier lookup (highest → lowest priority):
//   1. notification_templates.yaml    — per-agent user override
//   2. /config/notification_default_templates.yaml — user-editable global override
//   3. embedded notification_default_templates.yaml — compiled-in fallback

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed notification_default_templates.yaml
var embeddedDefaultTemplates []byte

const notificationTemplatesConfigVersion = 1

// ── per-agent overrides (notification_templates.yaml) ───────────────────────

type notificationTemplatesFile struct {
	Version   int                              `yaml:"version"`
	Templates map[string]NotificationTemplates `yaml:"templates"`
}

// loadAgentTemplates reads notification_templates.yaml.
// Returns an empty map if the file does not exist.
func loadAgentTemplates(configDir string) (map[string]NotificationTemplates, error) {
	raw, err := os.ReadFile(filepath.Join(configDir, "notification_templates.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]NotificationTemplates{}, nil
		}
		return nil, fmt.Errorf("read notification templates: %w", err)
	}
	var out notificationTemplatesFile
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse notification templates: %w", err)
	}
	if out.Templates == nil {
		out.Templates = map[string]NotificationTemplates{}
	}
	return out.Templates, nil
}

// saveAgentTemplates writes notification_templates.yaml.
// Agents with all-empty fields are pruned to keep the file tidy.
func saveAgentTemplates(configDir string, templates map[string]NotificationTemplates) error {
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	clean := make(map[string]NotificationTemplates, len(templates))
	for id, t := range templates {
		if t.hasContent() {
			clean[id] = t
		}
	}
	payload := notificationTemplatesFile{Version: notificationTemplatesConfigVersion, Templates: clean}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notification templates: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "notification_templates.yaml"), raw, 0o600); err != nil {
		return fmt.Errorf("write notification templates: %w", err)
	}
	return nil
}

// migrateAgentTemplateIDs rewrites agent IDs in notification_templates.yaml
// to match IDs renamed during a profile migration.
func migrateAgentTemplateIDs(configDir string, idMapping map[string]string) error {
	templates, err := loadAgentTemplates(configDir)
	if err != nil || len(templates) == 0 {
		return err
	}
	changed := false
	for oldID, newID := range idMapping {
		if t, ok := templates[oldID]; ok {
			templates[newID] = t
			delete(templates, oldID)
			changed = true
		}
	}
	if changed {
		return saveAgentTemplates(configDir, templates)
	}
	return nil
}

// ── global defaults (notification_default_templates.yaml) ───────────────────

type notificationDefaultTemplatesFile struct {
	Version   int                              `yaml:"version"`
	Providers map[string]NotificationTemplates `yaml:"providers"`
}

// InitDefaultTemplates loads provider-level default templates into the active
// override map used by DefaultTemplateFor. Call once at startup.
//
// If the user has placed a notification_default_templates.yaml in configDir
// that file takes precedence over the embedded binary version.
func InitDefaultTemplates(configDir string) error {
	raw := embeddedDefaultTemplates
	if userPath := filepath.Join(configDir, "notification_default_templates.yaml"); fileExists(userPath) {
		if data, err := os.ReadFile(userPath); err == nil {
			raw = data
		}
	}
	loaded, err := parseDefaultTemplates(raw)
	if err != nil {
		return err
	}
	fileDefaultTemplates = loaded
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// parseDefaultTemplates unmarshals raw YAML bytes into a provider map.
func parseDefaultTemplates(raw []byte) (map[string]NotificationTemplates, error) {
	var f notificationDefaultTemplatesFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse default templates: %w", err)
	}
	return f.Providers, nil
}
