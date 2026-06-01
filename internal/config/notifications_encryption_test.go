package config

import (
	"os"
	"strings"
	"testing"

	"chowkidar/internal/crypto"
)

// setupEncryption sets a test key and returns a cleanup function that clears it.
func setupEncryption(t *testing.T) {
	t.Helper()
	crypto.SetKey("test-encryption-key-for-notifications")
	t.Cleanup(func() { crypto.ClearKey() })
}

// TestNotificationServiceEncryptedOnSave verifies that service URLs are written
// as enc:v1: ciphertext and round-trip back to the original plaintext.
func TestNotificationServiceEncryptedOnSave(t *testing.T) {
	setupEncryption(t)
	dir := t.TempDir()

	profiles := []NotificationProfile{{
		ID:       "enc-svc-1",
		Name:     "Discord",
		Provider: "discord",
		Details:  map[string]string{"webhookid": "123456", "token": "mytoken"},
		Service:  "discord://123456/mytoken",
		Enabled:  true,
	}}

	if err := SaveNotificationProfiles(dir, profiles); err != nil {
		t.Fatalf("SaveNotificationProfiles() error = %v", err)
	}

	// Raw file must not contain the plaintext service URL.
	raw := readFile(t, dir, "notifications.yaml")
	if strings.Contains(raw, "discord://123456/mytoken") {
		t.Error("plaintext service URL found in notifications.yaml — encryption not applied")
	}
	if !strings.Contains(raw, "enc:v1:") {
		t.Error("expected enc:v1: prefix in notifications.yaml but not found")
	}

	// Loaded profile must have plaintext service URL restored.
	loaded, err := LoadNotificationProfiles(dir)
	if err != nil {
		t.Fatalf("LoadNotificationProfiles() error = %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(loaded))
	}
	if loaded[0].Service != "discord://123456/mytoken" {
		t.Errorf("service round-trip failed: got %q, want %q", loaded[0].Service, "discord://123456/mytoken")
	}
}

// TestNotificationDetailsEncryptedOnSave verifies that sensitive detail keys
// (token, password, webhookurl, etc.) are stored encrypted and decrypted on load.
func TestNotificationDetailsEncryptedOnSave(t *testing.T) {
	setupEncryption(t)
	dir := t.TempDir()

	profiles := []NotificationProfile{{
		ID:       "enc-details-1",
		Name:     "Email",
		Provider: "mailto",
		Details: map[string]string{
			"host":     "smtp.example.com",
			"port":     "587",
			"username": "user@example.com",
			"password": "supersecretpassword",
			"to":       "dest@example.com",
			"tls":      "auto",
		},
		Service: "mailto://user%40example.com:supersecretpassword@smtp.example.com:587/dest@example.com",
		Enabled: true,
	}}

	if err := SaveNotificationProfiles(dir, profiles); err != nil {
		t.Fatalf("SaveNotificationProfiles() error = %v", err)
	}

	raw := readFile(t, dir, "notifications.yaml")

	// Sensitive field must be encrypted.
	if strings.Contains(raw, "supersecretpassword") {
		t.Error("plaintext password found in notifications.yaml — details encryption not applied")
	}
	// Non-sensitive field must stay plaintext.
	if !strings.Contains(raw, "smtp.example.com") {
		t.Error("expected plaintext host in notifications.yaml")
	}

	// Loaded profile must have plaintext values restored.
	loaded, err := LoadNotificationProfiles(dir)
	if err != nil {
		t.Fatalf("LoadNotificationProfiles() error = %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(loaded))
	}
	if got := loaded[0].Details["password"]; got != "supersecretpassword" {
		t.Errorf("password round-trip failed: got %q, want %q", got, "supersecretpassword")
	}
	if got := loaded[0].Details["host"]; got != "smtp.example.com" {
		t.Errorf("host round-trip failed: got %q, want %q", got, "smtp.example.com")
	}
}

// TestNotificationNoEncryptionWithoutKey verifies that without CHOWKIDAR_SECRET_KEY
// values are stored as plaintext (backward-compatible behaviour).
func TestNotificationNoEncryptionWithoutKey(t *testing.T) {
	// Ensure no key is active for this test.
	crypto.ClearKey()
	dir := t.TempDir()

	profiles := []NotificationProfile{{
		ID:       "no-key-1",
		Name:     "Discord",
		Provider: "discord",
		Details:  map[string]string{"webhookid": "999", "token": "tok"},
		Service:  "discord://999/tok",
		Enabled:  true,
	}}

	if err := SaveNotificationProfiles(dir, profiles); err != nil {
		t.Fatalf("SaveNotificationProfiles() error = %v", err)
	}

	raw := readFile(t, dir, "notifications.yaml")
	if strings.Contains(raw, "enc:v1:") {
		t.Error("enc:v1: prefix found in notifications.yaml without a key — unexpected encryption")
	}
	if !strings.Contains(raw, "discord://999/tok") {
		t.Error("expected plaintext service URL in notifications.yaml when no key is set")
	}
}

// TestNotificationWrongKeyOnLoadClearsFields verifies that loading with the wrong key
// clears sensitive fields rather than crashing.
func TestNotificationWrongKeyOnLoadClearsFields(t *testing.T) {
	// Save with key A.
	crypto.SetKey("key-A")
	dir := t.TempDir()

	profiles := []NotificationProfile{{
		ID:       "rekey-1",
		Name:     "Gotify",
		Provider: "gotify",
		Details:  map[string]string{"host": "192.168.1.1:8080", "token": "secret-token"},
		Service:  "gotify://192.168.1.1:8080/secret-token",
		Enabled:  true,
	}}
	if err := SaveNotificationProfiles(dir, profiles); err != nil {
		t.Fatalf("SaveNotificationProfiles() error = %v", err)
	}

	// Load with key B — different key, decryption must fail gracefully.
	crypto.SetKey("key-B")
	t.Cleanup(func() { crypto.ClearKey() })

	loaded, err := LoadNotificationProfiles(dir)
	if err != nil {
		t.Fatalf("LoadNotificationProfiles() should not error on wrong key, got: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(loaded))
	}
	// Service and sensitive detail must be cleared, not crash.
	if loaded[0].Service != "" {
		t.Errorf("expected Service to be cleared on wrong key, got %q", loaded[0].Service)
	}
	if loaded[0].Details["token"] != "" {
		t.Errorf("expected details.token to be cleared on wrong key, got %q", loaded[0].Details["token"])
	}
	// Non-sensitive field must survive.
	if loaded[0].Details["host"] != "192.168.1.1:8080" {
		t.Errorf("expected details.host to be intact, got %q", loaded[0].Details["host"])
	}
}

// TestAllSensitiveDetailKeysEncrypted verifies every key in sensitiveDetailKeys
// is encrypted on save and decrypted on load.
func TestAllSensitiveDetailKeysEncrypted(t *testing.T) {
	setupEncryption(t)
	dir := t.TempDir()

	// Build a profile that has every sensitive key populated.
	details := map[string]string{}
	for k := range sensitiveDetailKeys {
		details[k] = "plaintext-value-for-" + k
	}
	// Minimal fields required to pass normalisation for a webhook provider.
	details["url"] = "https://example.com/hook"

	profiles := []NotificationProfile{{
		ID:       "all-keys-1",
		Name:     "Webhook",
		Provider: "webhook",
		Details:  details,
		Service:  "https://example.com/hook",
		Enabled:  true,
	}}

	if err := SaveNotificationProfiles(dir, profiles); err != nil {
		t.Fatalf("SaveNotificationProfiles() error = %v", err)
	}

	raw := readFile(t, dir, "notifications.yaml")
	for k := range sensitiveDetailKeys {
		if strings.Contains(raw, "plaintext-value-for-"+k) {
			t.Errorf("sensitive key %q stored as plaintext in notifications.yaml", k)
		}
	}

	loaded, err := LoadNotificationProfiles(dir)
	if err != nil {
		t.Fatalf("LoadNotificationProfiles() error = %v", err)
	}
	for k := range sensitiveDetailKeys {
		got := loaded[0].Details[k]
		// "url" is explicitly set to a valid webhook URL for normalization to pass.
		want := "plaintext-value-for-" + k
		if k == "url" {
			want = "https://example.com/hook"
		}
		if got != want {
			t.Errorf("details[%q] round-trip failed: got %q, want %q", k, got, want)
		}
	}
}

// readFile is a test helper that reads a file from dir and returns its content.
func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	import_path := dir + "/" + name
	data, err := os.ReadFile(import_path)
	if err != nil {
		t.Fatalf("readFile(%q): %v", import_path, err)
	}
	return string(data)
}
