package notify

import "testing"

func TestNewParsesServiceCSV(t *testing.T) {
	n := New(" mailto://user:pass@example.com , discord://token@id , slack://token@hook , telegram://bot@chat , ntfy://host/topic , pover://user@token , gotify://host/token ")
	if !n.Enabled {
		t.Fatal("expected notifier to be enabled")
	}
	if len(n.Services) != 7 {
		t.Fatalf("expected 7 services, got %d", len(n.Services))
	}
	if n.Services[0] != "mailto://user:pass@example.com" || n.Services[1] != "discord://token@id" || n.Services[2] != "slack://token@hook" || n.Services[3] != "telegram://bot@chat" || n.Services[4] != "ntfy://host/topic" || n.Services[5] != "pover://user@token" || n.Services[6] != "gotify://host/token" {
		t.Fatalf("services not trimmed correctly: %#v", n.Services)
	}
}
