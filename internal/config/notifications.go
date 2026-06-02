package config

import (
	"chowkidar/internal/crypto"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
	"strings"

	"gopkg.in/yaml.v3"
)

type NotificationTitleTemplates struct {
	BootUp             string `yaml:"bootUp,omitempty"             json:"bootUp,omitempty"`
	UnhealthyDetected  string `yaml:"unhealthyDetected,omitempty"  json:"unhealthyDetected,omitempty"`
	Retrying           string `yaml:"retrying,omitempty"           json:"retrying,omitempty"`
	Cooldown           string `yaml:"cooldown,omitempty"           json:"cooldown,omitempty"`
	ContainerRecovered string `yaml:"containerRecovered,omitempty" json:"containerRecovered,omitempty"`
	ActionFailed       string `yaml:"actionFailed,omitempty"       json:"actionFailed,omitempty"`
	RetryLimit         string `yaml:"retryLimit,omitempty"         json:"retryLimit,omitempty"`
}

type NotificationTemplates struct {
	Titles             NotificationTitleTemplates `yaml:"titles,omitempty"             json:"titles,omitempty"`
	BootUp             string                     `yaml:"bootUp,omitempty"             json:"bootUp,omitempty"`
	UnhealthyDetected  string                     `yaml:"unhealthyDetected,omitempty"  json:"unhealthyDetected,omitempty"`
	Retrying           string                     `yaml:"retrying,omitempty"           json:"retrying,omitempty"`
	Cooldown           string                     `yaml:"cooldown,omitempty"           json:"cooldown,omitempty"`
	ContainerRecovered string                     `yaml:"containerRecovered,omitempty" json:"containerRecovered,omitempty"`
	ActionFailed       string                     `yaml:"actionFailed,omitempty"       json:"actionFailed,omitempty"`
	RetryLimit         string                     `yaml:"retryLimit,omitempty"         json:"retryLimit,omitempty"`
}

// hasContent reports whether any template field is non-empty.
func (t NotificationTemplates) hasContent() bool {
	return t.BootUp != "" || t.UnhealthyDetected != "" || t.Retrying != "" ||
		t.Cooldown != "" || t.ContainerRecovered != "" || t.ActionFailed != "" ||
		t.RetryLimit != ""
}

// pickBody returns the body template string for event from a NotificationTemplates set.
func (t NotificationTemplates) pickBody(event string) string {
	switch event {
	case "startup":
		return t.BootUp
	case "unhealthy detected":
		return t.UnhealthyDetected
	case "retrying":
		return t.Retrying
	case "cooldown":
		return t.Cooldown
	case "container recovered":
		return t.ContainerRecovered
	case "action failed":
		return t.ActionFailed
	case "retry limit reached":
		return t.RetryLimit
	}
	return ""
}

// pickTitle returns the title template string for event from a NotificationTemplates set.
func (t NotificationTemplates) pickTitle(event string) string {
	switch event {
	case "startup":
		return t.Titles.BootUp
	case "unhealthy detected":
		return t.Titles.UnhealthyDetected
	case "retrying":
		return t.Titles.Retrying
	case "cooldown":
		return t.Titles.Cooldown
	case "container recovered":
		return t.Titles.ContainerRecovered
	case "action failed":
		return t.Titles.ActionFailed
	case "retry limit reached":
		return t.Titles.RetryLimit
	}
	return ""
}

// AppliedTemplate returns the message body to send for event.
// Priority: (1) profile's saved custom template, (2) provider-specific default,
// (3) generic plain-text default.
func (p NotificationProfile) AppliedTemplate(event string) string {
	if custom := p.Templates.pickBody(event); strings.TrimSpace(custom) != "" {
		return custom
	}
	return DefaultTemplateFor(p.Provider, event)
}

// AppliedTitleTemplate returns the title template string for event.
// Priority: (1) profile's saved custom title, (2) provider-specific default,
// (3) generic plain-text default.
func (p NotificationProfile) AppliedTitleTemplate(event string) string {
	if custom := p.Templates.pickTitle(event); strings.TrimSpace(custom) != "" {
		return custom
	}
	return DefaultTitleTemplateFor(p.Provider, event)
}

type NotificationProfile struct {
	ID        string                `yaml:"id" json:"id"`
	Name      string                `yaml:"name" json:"name"`
	Provider  string                `yaml:"provider,omitempty" json:"provider,omitempty"`
	Details   map[string]string     `yaml:"details,omitempty" json:"details,omitempty"`
	Service   string                `yaml:"service" json:"service"`
	Enabled   bool                  `yaml:"enabled" json:"enabled"`
	IsDefault bool                  `yaml:"is_default,omitempty" json:"is_default,omitempty"`
	Templates NotificationTemplates `yaml:"templates,omitempty" json:"templates,omitempty"`

	// Rate limiting & suspension
	BurstLimit         int        `yaml:"burstLimit,omitempty"         json:"burstLimit,omitempty"`
	BurstWindowMinutes int        `yaml:"burstWindowMinutes,omitempty" json:"burstWindowMinutes,omitempty"`
	DailyLimit         int        `yaml:"dailyLimit,omitempty"         json:"dailyLimit,omitempty"`
	// OnLimitAction: "auto-suspend" (default) | "drop" | "fallback"
	OnLimitAction      string     `yaml:"onLimitAction,omitempty"      json:"onLimitAction,omitempty"`
	// AutoSuspendOnError: auto-suspend to midnight when a quota/rate-limit error is detected
	AutoSuspendOnError  bool       `yaml:"autoSuspendOnError,omitempty"  json:"autoSuspendOnError,omitempty"`
	// SuspendedUntil: non-nil means the profile is suspended; nil or past time = active
	SuspendedUntil      *time.Time `yaml:"suspendedUntil,omitempty"      json:"suspendedUntil,omitempty"`
	// LastRateLimitError + LastRateLimitAt: persisted so the UI can show the error after restart
	LastRateLimitError  string     `yaml:"lastRateLimitError,omitempty"  json:"lastRateLimitError,omitempty"`
	LastRateLimitAt     *time.Time `yaml:"lastRateLimitAt,omitempty"     json:"lastRateLimitAt,omitempty"`
}

const notificationsConfigVersion = 1

type NotificationsFile struct {
	Version  int                   `yaml:"version"`
	Profiles []NotificationProfile `yaml:"profiles"`
}

func LoadNotificationProfiles(configDir string) ([]NotificationProfile, error) {
	filePath := filepath.Join(configDir, "notifications.yaml")
	raw, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []NotificationProfile{}, nil
		}
		return nil, fmt.Errorf("read notifications file: %w", err)
	}

	var out NotificationsFile
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse notifications yaml: %w", err)
	}
	if out.Profiles == nil {
		out.Profiles = []NotificationProfile{}
	}

	// Run any pending migrations; write back if the file changed.
	if migrated, updated, idMapping := migrateNotificationProfiles(out.Version, out.Profiles); migrated {
		out.Profiles = updated
		_ = saveNotificationProfilesVersioned(configDir, out.Profiles)
		if len(idMapping) > 0 {
			_ = applyNotificationIDMappingToJobs(configDir, idMapping)
			// Also rename IDs in the templates store so overrides survive migration.
			_ = migrateAgentTemplateIDs(configDir, idMapping)
		}
	}

	// Decrypt service URLs and sensitive details in memory so callers always work with plaintext.
	// On key mismatch, clear the field so the app still starts; user must re-enter.
	for i, p := range out.Profiles {
		if decrypted, err := crypto.MaybeDecrypt(p.Service); err != nil {
			out.Profiles[i].Service = ""
		} else {
			out.Profiles[i].Service = decrypted
		}
		out.Profiles[i].Details = decryptDetails(p.Details)
	}

	// Merge per-agent template overrides from notification_templates.yaml into
	// each profile so callers see the full picture in one place.
	if agentTemplates, err := loadAgentTemplates(configDir); err == nil {
		for i, p := range out.Profiles {
			if t, ok := agentTemplates[p.ID]; ok {
				out.Profiles[i].Templates = t
			}
		}
	}

	return out.Profiles, nil
}

func SaveNotificationProfiles(configDir string, profiles []NotificationProfile) error {
	normalized := make([]NotificationProfile, 0, len(profiles))
	for i, p := range profiles {
		n, err := normalizeNotificationProfile(p, i)
		if err != nil {
			return err
		}
		normalized = append(normalized, n)
	}
	// Ensure at most one profile is the default.
	defaultSeen := false
	for i := range normalized {
		if normalized[i].IsDefault {
			if defaultSeen {
				normalized[i].IsDefault = false
			}
			defaultSeen = true
		}
	}

	// Extract per-agent template overrides and write to notification_templates.yaml.
	// Only include agents that have at least one non-empty field.
	agentTemplates := make(map[string]NotificationTemplates, len(normalized))
	for _, p := range normalized {
		if p.ID != "" {
			agentTemplates[p.ID] = p.Templates
		}
	}
	if err := saveAgentTemplates(configDir, agentTemplates); err != nil {
		return fmt.Errorf("save notification templates: %w", err)
	}

	return saveNotificationProfilesVersioned(configDir, normalized)
}

// sensitiveDetailKeys is the set of details map keys that contain credentials
// and must be encrypted at rest.
var sensitiveDetailKeys = map[string]bool{
	"password":   true,
	"token":      true,
	"bottoken":   true,
	"webhookurl": true,
	"webhookid":  true,
	"tokena":     true,
	"tokenb":     true,
	"tokenc":     true,
	"user":       true,
	"url":        true,
	"dkimkey":    true,
}

// encryptDetails returns a copy of details with sensitive keys encrypted.
func encryptDetails(details map[string]string) (map[string]string, error) {
	if len(details) == 0 {
		return details, nil
	}
	out := make(map[string]string, len(details))
	for k, v := range details {
		if sensitiveDetailKeys[k] {
			enc, err := crypto.MaybeEncrypt(v)
			if err != nil {
				return nil, fmt.Errorf("encrypt details.%s: %w", k, err)
			}
			out[k] = enc
		} else {
			out[k] = v
		}
	}
	return out, nil
}

// decryptDetails returns a copy of details with sensitive keys decrypted.
// On key mismatch the field is cleared so the app continues running.
func decryptDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return details
	}
	out := make(map[string]string, len(details))
	for k, v := range details {
		if sensitiveDetailKeys[k] {
			if dec, err := crypto.MaybeDecrypt(v); err != nil {
				out[k] = ""
			} else {
				out[k] = dec
			}
		} else {
			out[k] = v
		}
	}
	return out
}

func saveNotificationProfilesVersioned(configDir string, profiles []NotificationProfile) error {
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Strip templates and encrypt service URLs and sensitive details before writing to disk.
	stripped := make([]NotificationProfile, len(profiles))
	for i, p := range profiles {
		enc, err := crypto.MaybeEncrypt(p.Service)
		if err != nil {
			return fmt.Errorf("encrypt service for profile %q: %w", p.ID, err)
		}
		encDetails, err := encryptDetails(p.Details)
		if err != nil {
			return fmt.Errorf("encrypt details for profile %q: %w", p.ID, err)
		}
		stripped[i] = p
		stripped[i].Service = enc
		stripped[i].Details = encDetails
		stripped[i].Templates = NotificationTemplates{}
	}

	payload := NotificationsFile{Version: notificationsConfigVersion, Profiles: stripped}
	raw, err := yaml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notifications yaml: %w", err)
	}

	filePath := filepath.Join(configDir, "notifications.yaml")
	if err := os.WriteFile(filePath, raw, 0o600); err != nil {
		return fmt.Errorf("write notifications yaml: %w", err)
	}
	return nil
}

// migrateNotificationProfiles applies schema migrations up to
// notificationsConfigVersion. Returns (migrated, updatedProfiles, oldID→newID
// mapping). Add a new "if fileVersion < N" block for each future migration.
func migrateNotificationProfiles(fileVersion int, profiles []NotificationProfile) (migrated bool, result []NotificationProfile, idMapping map[string]string) {
	idMapping = make(map[string]string)
	if fileVersion < 1 {
		// v0→v1: rewrite legacy profile-* and p-ui-* IDs to notification-agent-*.
		for i, p := range profiles {
			var newID string
			switch {
			case strings.HasPrefix(p.ID, "profile-"):
				newID = "notification-agent-" + strings.TrimPrefix(p.ID, "profile-")
			case strings.HasPrefix(p.ID, "p-ui-"):
				newID = "notification-agent-" + strings.TrimPrefix(p.ID, "p-ui-")
			}
			if newID != "" {
				idMapping[p.ID] = newID
				profiles[i].ID = newID
				migrated = true
			}
		}
	}
	return migrated, profiles, idMapping
}

// applyNotificationIDMappingToJobs rewrites notification profile ID references
// inside jobs.yaml when those IDs were renamed by a notification migration.
func applyNotificationIDMappingToJobs(configDir string, idMapping map[string]string) error {
	jobs, err := LoadJobs(configDir)
	if err != nil {
		return err
	}
	changed := false
	for i, j := range jobs {
		for k, nid := range j.Notifications {
			if newID, ok := idMapping[nid]; ok {
				jobs[i].Notifications[k] = newID
				changed = true
			}
		}
	}
	if changed {
		return saveJobsVersioned(configDir, jobs)
	}
	return nil
}

func deriveProviderFromService(in *NotificationProfile) {
	if in.Provider != "" || in.Service == "" {
		return
	}
	in.Provider = parseServiceScheme(in.Service)
	if in.Provider == "tgram" {
		in.Provider = "telegram"
	}
}

func buildProfileServiceURL(in *NotificationProfile) error {
	if in.Provider == "" {
		return nil
	}
	if !isSupportedNotificationProvider(in.Provider) {
		return fmt.Errorf("unsupported notification provider %q", in.Provider)
	}
	if err := validateNotificationDetails(in.Provider, in.Details); err != nil {
		return err
	}
	if in.Service == "" || needsServiceRebuild(in.Provider, in.Service) {
		built, err := buildServiceURLFromProvider(in.Provider, in.Details)
		if err != nil {
			return err
		}
		in.Service = built
	}
	return nil
}

func validateSchemeMatch(in NotificationProfile) error {
	if in.Provider == "" || in.Provider == "webhook" {
		return nil
	}
	scheme := parseServiceScheme(in.Service)
	if scheme == "" || scheme == in.Provider {
		return nil
	}
	schemeMap := map[string]string{
		"mailto":   "mailtos",
		"telegram": "tgram",
		"slack":    "https",
		"ntfy":     "ntfys",
	}
	expected, ok := schemeMap[in.Provider]
	if !ok || expected != scheme {
		return fmt.Errorf("notification provider %q does not match service scheme %q", in.Provider, scheme)
	}
	return nil
}

func normalizeNotificationProfile(in NotificationProfile, idx int) (NotificationProfile, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.Name = strings.TrimSpace(in.Name)
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	in.Service = strings.TrimSpace(in.Service)
	in.Details = normalizeNotificationDetails(in.Details)

	if in.ID == "" {
		in.ID = fmt.Sprintf("profile-%d", idx+1)
	}
	if in.Name == "" {
		return NotificationProfile{}, fmt.Errorf("notification profile name is required")
	}
	deriveProviderFromService(&in)
	if err := buildProfileServiceURL(&in); err != nil {
		return NotificationProfile{}, err
	}
	if in.Service == "" {
		return NotificationProfile{}, fmt.Errorf("notification profile service is required")
	}
	if err := validateSchemeMatch(in); err != nil {
		return NotificationProfile{}, err
	}
	return in, nil
}

// needsServiceRebuild returns true when a saved service URL uses a legacy format
// that would be rejected by Apprise and should be regenerated from details.
func needsServiceRebuild(provider, service string) bool {
	if provider == "telegram" {
		// Migrate telegram:// → tgram:// (Apprise 1.10+ only recognises tgram://)
		// Also migrate the old @chatid format to /chatid
		return strings.HasPrefix(strings.ToLower(service), "telegram://")
	}
	return false
}

var (
	discordWebhookRE = regexp.MustCompile(`discord\.com/api/webhooks/([^/\s?]+)/([^/\s?]+)`)
	httpSchemeRE     = regexp.MustCompile(`^https?://`)
	nameEmailRE      = regexp.MustCompile(`^(.+?)\s*<([^>]+)>\s*$`)
	serviceSchemeRE  = regexp.MustCompile(`^([a-z][a-z0-9+.-]*)://`)
)

func normalizeNotificationDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(details))
	for k, v := range details {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		val := strings.TrimSpace(v)
		// Remove protocol and trailing slashes from host/server fields.
		if key == "host" || key == "server" {
			val = httpSchemeRE.ReplaceAllString(val, "")
			val = strings.TrimRight(val, "/")
		}
		out[key] = val
	}
	return out
}

func parseServiceScheme(service string) string {
	match := serviceSchemeRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(service)))
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func isSupportedNotificationProvider(provider string) bool {
	switch provider {
	case "discord", "mailto", "slack", "telegram", "ntfy", "pover", "gotify", "webhook":
		return true
	default:
		return false
	}
}

func validateNotificationDetails(provider string, details map[string]string) error {
	if len(details) == 0 {
		return nil
	}
	requiredByProvider := map[string][]string{
		"discord":  {},
		"slack":    {"tokena", "tokenb", "tokenc"},
		"telegram": {"bottoken", "chatid"},
		"mailto":   {"to"},
		"ntfy":     {"host", "topic"},
		"pover":    {"user", "token"},
		"gotify":   {"host", "token"},
		"webhook":  {"url"},
	}
	// Slack webhookurl disables the legacy token requirement.
	if provider == "slack" && strings.TrimSpace(details["webhookurl"]) != "" {
		return nil
	}
	for _, key := range requiredByProvider[provider] {
		if strings.TrimSpace(details[key]) == "" {
			return fmt.Errorf("notification provider %q requires details.%s", provider, key)
		}
	}
	return nil
}

// buildServiceURLFromProvider dispatches to per-provider URL builders.
func buildServiceURLFromProvider(provider string, details map[string]string) (string, error) {
	if err := validateNotificationDetails(provider, details); err != nil {
		return "", err
	}
	switch provider {
	case "discord":
		return buildDiscordURL(details)
	case "slack":
		return buildSlackURL(details)
	case "telegram":
		return buildTelegramURL(details)
	case "mailto":
		return buildMailtoURL(details)
	case "ntfy":
		return buildNtfyURL(details)
	case "pover":
		return buildPoverURL(details)
	case "gotify":
		return buildGotifyURL(details), nil
	case "webhook":
		return details["url"], nil
	default:
		return "", fmt.Errorf("unsupported notification provider %q", provider)
	}
}

func buildDiscordURL(details map[string]string) (string, error) {
	if whu := strings.TrimSpace(details["webhookurl"]); whu != "" {
		m := discordWebhookRE.FindStringSubmatch(whu)
		if m == nil {
			return "", fmt.Errorf("discord webhook URL must be https://discord.com/api/webhooks/ID/TOKEN")
		}
		return fmt.Sprintf("discord://%s/%s", m[1], m[2]), nil
	}
	wid := strings.TrimSpace(details["webhookid"])
	tok := strings.TrimSpace(details["token"])
	if wid == "" || tok == "" {
		return "", fmt.Errorf("discord requires a webhook URL or both webhook ID and token")
	}
	return fmt.Sprintf("discord://%s/%s", wid, tok), nil
}

func buildSlackURL(details map[string]string) (string, error) {
	whu := strings.TrimSpace(details["webhookurl"])
	if whu == "" {
		return fmt.Sprintf("slack://%s/%s/%s", details["tokena"], details["tokenb"], details["tokenc"]), nil
	}
	var params []string
	if u := strings.TrimSpace(details["username"]); u != "" {
		params = append(params, "username="+url.QueryEscape(u))
	}
	if e := strings.TrimSpace(details["iconemoji"]); e != "" {
		params = append(params, "iconemoji="+url.QueryEscape(e))
	}
	if c := strings.TrimSpace(details["channel"]); c != "" {
		params = append(params, "channel="+url.QueryEscape(c))
	}
	if len(params) == 0 {
		return whu, nil
	}
	sep := "?"
	if strings.Contains(whu, "?") {
		sep = "&"
	}
	return whu + sep + strings.Join(params, "&"), nil
}

func buildTelegramURL(details map[string]string) (string, error) {
	base := fmt.Sprintf("tgram://%s/%s", details["bottoken"], details["chatid"])
	var params []string
	if tid := strings.TrimSpace(details["threadid"]); tid != "" {
		params = append(params, "thread="+url.QueryEscape(tid))
	}
	if s := strings.ToLower(strings.TrimSpace(details["silent"])); s == "yes" || s == "true" || s == "1" {
		params = append(params, "silent=yes")
	}
	return base + joinQuery(params), nil
}

func buildMailtoURL(details map[string]string) (string, error) {
	scheme := "mailto"
	if strings.ToLower(strings.TrimSpace(details["tls"])) == "ssl" {
		scheme = "mailtos"
	}
	host := details["host"]
	if p := strings.TrimSpace(details["port"]); p != "" {
		host += ":" + p
	}
	userinfo := host
	if details["username"] != "" {
		userinfo = url.PathEscape(details["username"]) + ":" + url.PathEscape(details["password"]) + "@" + host
	}
	if strings.TrimSpace(userinfo) == "" {
		return "", fmt.Errorf("notification provider %q requires details.host", "mailto")
	}
	to := strings.TrimSpace(details["to"])
	if to == "" {
		return "", fmt.Errorf("notification provider %q requires details.to", "mailto")
	}
	rawURL := fmt.Sprintf("%s://%s/%s", scheme, userinfo, to)

	var params []string
	if strings.ToLower(strings.TrimSpace(details["tls"])) == "starttls" {
		params = append(params, "secure=startssl")
	}
	if from := strings.TrimSpace(details["from"]); from != "" {
		if m := nameEmailRE.FindStringSubmatch(from); m != nil {
			params = append(params,
				"name="+url.QueryEscape(strings.TrimSpace(m[1])),
				"from="+url.QueryEscape(strings.TrimSpace(m[2])),
			)
		} else {
			params = append(params, "from="+url.QueryEscape(from))
		}
	}
	if cc := strings.TrimSpace(details["cc"]); cc != "" {
		params = append(params, "cc="+url.QueryEscape(cc))
	}
	if bcc := strings.TrimSpace(details["bcc"]); bcc != "" {
		params = append(params, "bcc="+url.QueryEscape(bcc))
	}
	for _, kv := range [][2]string{
		{"dkimDomain", "dkim_domain"}, {"dkimSelector", "dkim_selector"}, {"dkimKey", "dkim_key"},
		{"dkimHashAlgo", "dkim_hash_algo"}, {"dkimHeaderKeys", "dkim_header_keys"},
		{"dkimSkipHeaderKeys", "dkim_skip_header_keys"},
	} {
		if v := strings.TrimSpace(details[kv[0]]); v != "" {
			params = append(params, kv[1]+"="+url.QueryEscape(v))
		}
	}
	if s := strings.TrimSpace(details["customSubject"]); s != "" {
		params = append(params, "subject="+url.QueryEscape(s))
	}
	return rawURL + joinQuery(params), nil
}

func buildNtfyURL(details map[string]string) (string, error) {
	host := httpSchemeRE.ReplaceAllString(strings.TrimRight(details["host"], "/"), "")
	topic := strings.TrimLeft(details["topic"], "/")
	base := fmt.Sprintf("ntfy://%s/%s", host, topic)

	var params []string
	if p := strings.TrimSpace(details["priority"]); p != "" {
		params = append(params, "priority="+url.QueryEscape(p))
	}
	if icon := strings.TrimSpace(details["iconUrl"]); icon != "" {
		params = append(params, "icon="+url.QueryEscape(icon))
	}
	if auth := strings.TrimSpace(details["authMethod"]); auth != "" && auth != "none" {
		params = append(params, "auth="+url.QueryEscape(auth))
		switch auth {
		case "userpass":
			if u := strings.TrimSpace(details["username"]); u != "" {
				params = append(params, "user="+url.QueryEscape(u))
			}
			if pw := strings.TrimSpace(details["password"]); pw != "" {
				params = append(params, "pass="+url.QueryEscape(pw))
			}
		case "token":
			if t := strings.TrimSpace(details["token"]); t != "" {
				params = append(params, "token="+url.QueryEscape(t))
			}
		}
	}
	return base + joinQuery(params), nil
}

func buildPoverURL(details map[string]string) (string, error) {
	base := fmt.Sprintf("pover://%s@%s", details["user"], details["token"])
	var params []string
	for _, kv := range [][2]string{
		{"device", "device"}, {"title", "title"}, {"priority", "priority"},
		{"sound", "sound"}, {"ttl", "ttl"},
	} {
		if v := strings.TrimSpace(details[kv[0]]); v != "" {
			params = append(params, kv[1]+"="+url.QueryEscape(v))
		}
	}
	return base + joinQuery(params), nil
}

func buildGotifyURL(details map[string]string) string {
	host := httpSchemeRE.ReplaceAllString(strings.TrimRight(details["host"], "/"), "")
	return fmt.Sprintf("gotify://%s/%s", host, details["token"])
}

// joinQuery assembles a URL query string from a slice of pre-encoded key=value pairs.
func joinQuery(params []string) string {
	if len(params) == 0 {
		return ""
	}
	return "?" + strings.Join(params, "&")
}

func NotificationProviderFields(provider string) []string {
	fields := map[string][]string{
		"discord":  {"webhookurl", "webhookid", "token"},
		"slack":    {"tokena", "tokenb", "tokenc"},
		"telegram": {"bottoken", "chatid", "threadid", "silent"},
		"mailto":   {"from", "host", "password", "port", "tls", "to", "username"},
		"ntfy":     {"host", "topic"},
		"pover":    {"user", "token"},
		"gotify":   {"host", "token"},
		"webhook":  {"url"},
	}
	out := append([]string(nil), fields[provider]...)
	sort.Strings(out)
	return out
}
