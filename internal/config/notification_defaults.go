package config

// fileDefaultTemplates holds provider → NotificationTemplates loaded from
// the embedded notification_default_templates.yaml (or a user override at
// /config/notification_default_templates.yaml). Set by InitDefaultTemplates
// at startup. nil means the file was not loaded.
var fileDefaultTemplates map[string]NotificationTemplates

// DefaultTemplateFor returns the best matching template body for (provider, event).
// It checks the loaded file defaults first, then falls back to the plain-text
// "default" provider entry. Returns "" if nothing is found.
func DefaultTemplateFor(provider, event string) string {
	if fileDefaultTemplates == nil {
		return ""
	}
	if t, ok := fileDefaultTemplates[provider]; ok {
		if v := t.pickBody(event); v != "" {
			return v
		}
	}
	if t, ok := fileDefaultTemplates["default"]; ok {
		return t.pickBody(event)
	}
	return ""
}

// DefaultTitleTemplateFor returns the best matching title template for (provider, event).
func DefaultTitleTemplateFor(provider, event string) string {
	if fileDefaultTemplates == nil {
		return ""
	}
	if t, ok := fileDefaultTemplates[provider]; ok {
		if v := t.pickTitle(event); v != "" {
			return v
		}
	}
	if t, ok := fileDefaultTemplates["default"]; ok {
		return t.pickTitle(event)
	}
	return ""
}
