package config

import (
	"os"
	"testing"
)

func TestNotificationProfileRoundTripAndValidation(t *testing.T) {
	dir := t.TempDir()
	profiles := []NotificationProfile{{
		ID:       "p1",
		Name:     "Discord",
		Provider: "discord",
		Details: map[string]string{
			"token":     "token",
			"webhookid": "id",
		},
		Service: "discord://token@id",
		Enabled: true,
	}}
	if err := SaveNotificationProfiles(dir, profiles); err != nil {
		t.Fatalf("SaveNotificationProfiles() error = %v", err)
	}
	loaded, err := LoadNotificationProfiles(dir)
	if err != nil {
		t.Fatalf("LoadNotificationProfiles() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "Discord" || loaded[0].Service != "discord://token@id" || loaded[0].Provider != "discord" {
		t.Fatalf("round-trip mismatch: %#v", loaded)
	}
	if err := SaveNotificationProfiles(dir, []NotificationProfile{{Name: "bad"}}); err == nil {
		t.Fatal("expected error when service is empty")
	}
	if err := SaveNotificationProfiles(dir, []NotificationProfile{{Name: "bad provider", Provider: "discord", Details: map[string]string{"token": "abc"}}}); err == nil {
		t.Fatal("expected error when provider details are incomplete")
	}
}

func TestMigrateNotificationProfilesV0ToV1RewritesLegacyPrefixes(t *testing.T) {
	input := []NotificationProfile{
		{ID: "p-ui-1", Name: "Discord", Service: "discord://token/id", Enabled: true},
		{ID: "profile-1779787781388", Name: "Telegram", Service: "tgram://bot/chat", Enabled: true},
		{ID: "notification-agent-999", Name: "Existing", Service: "discord://token/id", Enabled: true},
	}
	migrated, result, idMapping := migrateNotificationProfiles(0, input)
	if !migrated {
		t.Fatal("expected migrated=true")
	}
	if result[0].ID != "notification-agent-1" {
		t.Errorf("p-ui-1 → got %s", result[0].ID)
	}
	if result[1].ID != "notification-agent-1779787781388" {
		t.Errorf("profile-1779787781388 → got %s", result[1].ID)
	}
	if result[2].ID != "notification-agent-999" {
		t.Errorf("already-prefixed should be unchanged, got %s", result[2].ID)
	}
	if len(idMapping) != 2 {
		t.Errorf("expected 2 mappings, got %d: %v", len(idMapping), idMapping)
	}
}

func TestMigrateNotificationProfilesCurrentVersionNoOp(t *testing.T) {
	input := []NotificationProfile{
		{ID: "notification-agent-1", Name: "Discord", Service: "discord://token/id", Enabled: true},
	}
	migrated, _, _ := migrateNotificationProfiles(notificationsConfigVersion, input)
	if migrated {
		t.Fatal("expected no migration for current version")
	}
}

func TestLoadNotificationProfilesMigratesOnDiskAndUpdatesJobs(t *testing.T) {
	dir := t.TempDir()

	// Write a v0 notifications.yaml with legacy IDs.
	notifYAML := "profiles:\n  - id: profile-111\n    name: Discord\n    service: discord://token/id\n    enabled: true\n"
	if err := os.WriteFile(dir+"/notifications.yaml", []byte(notifYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a jobs.yaml that references the old profile ID.
	jobsYAML := "version: 1\njobs:\n  - id: job-999\n    name: test\n    enabled: true\n    action: restart\n    notifications:\n      - profile-111\n    containerNameFilter: test\n    createdAt: 2026-01-01T00:00:00Z\n    updatedAt: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(dir+"/jobs.yaml", []byte(jobsYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := LoadNotificationProfiles(dir)
	if err != nil {
		t.Fatalf("LoadNotificationProfiles error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].ID != "notification-agent-111" {
		t.Fatalf("expected migrated ID notification-agent-111, got %v", profiles)
	}

	// Jobs file should also have the updated reference.
	jobs, err := LoadJobs(dir)
	if err != nil {
		t.Fatalf("LoadJobs error = %v", err)
	}
	if len(jobs) != 1 || len(jobs[0].Notifications) != 1 || jobs[0].Notifications[0] != "notification-agent-111" {
		t.Fatalf("expected job notification reference to be migrated, got %v", jobs[0].Notifications)
	}
}

func TestDockerHostProfileRoundTripAndValidation(t *testing.T) {
	dir := t.TempDir()
	hosts := []DockerHostProfile{{ID: "h1", Name: "Local Socket", Type: "socket", Endpoint: "/var/run/docker.sock", Enabled: true}}
	if err := SaveDockerHostProfiles(dir, hosts); err != nil {
		t.Fatalf("SaveDockerHostProfiles() error = %v", err)
	}
	loaded, err := LoadDockerHostProfiles(dir)
	if err != nil {
		t.Fatalf("LoadDockerHostProfiles() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Endpoint != "/var/run/docker.sock" {
		t.Fatalf("round-trip mismatch: %#v", loaded)
	}
	if err := SaveDockerHostProfiles(dir, []DockerHostProfile{{Name: "bad", Type: "socket", Endpoint: "docker.sock"}}); err == nil {
		t.Fatal("expected error when socket endpoint is not absolute")
	}
}

func TestScriptEntryRoundTripAndValidation(t *testing.T) {
	dir := t.TempDir()
	scripts := []ScriptEntry{{ID: "s1", Name: "Heal", Path: "/config/scripts/heal.sh", Enabled: true}}
	if err := SaveScriptEntries(dir, scripts); err != nil {
		t.Fatalf("SaveScriptEntries() error = %v", err)
	}
	loaded, err := LoadScriptEntries(dir)
	if err != nil {
		t.Fatalf("LoadScriptEntries() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Path != "/config/scripts/heal.sh" {
		t.Fatalf("round-trip mismatch: %#v", loaded)
	}
	if err := SaveScriptEntries(dir, []ScriptEntry{{Name: "bad"}}); err == nil {
		t.Fatal("expected error when path is empty")
	}
}
