package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chowkidar/internal/config"
)

func newMaintenanceTestServer(t *testing.T, jobs []config.Job, hosts []config.DockerHostProfile) (*app, *httptest.Server) {
	t.Helper()

	configDir := t.TempDir()
	a := &app{
		cfg:                config.Config{ConfigDir: configDir},
		docker:             &fakeDockerClient{},
		jobs:               jobs,
		dockerHosts:        hosts,
		maintenanceWindows: []config.MaintenanceWindow{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/maintenance", a.handleMaintenance)
	mux.HandleFunc("/api/maintenance/active", a.handleMaintenanceActive)
	mux.HandleFunc("/api/maintenance/", a.handleMaintenanceByID)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return a, srv
}

func TestMaintenanceAPICRUDRoundTrip(t *testing.T) {
	jobs := []config.Job{{ID: "job-1", Name: "sonarr", Enabled: true, ContainerNameFilter: "sonarr"}}
	_, srv := newMaintenanceTestServer(t, jobs, nil)

	createBody := []byte(`{"title":"Backup window","active":true,"jobIDs":["job-1"],"strategy":"manual","timezone":"UTC"}`)
	resp := mustHTTPStatus(t, http.MethodPost, srv.URL+"/api/maintenance", createBody, http.StatusCreated)
	var created config.MaintenanceWindow
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created window error = %v", err)
	}
	_ = resp.Body.Close()
	if created.ID == "" || created.Title != "Backup window" {
		t.Fatalf("unexpected created window: %#v", created)
	}

	resp = mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/maintenance", nil, http.StatusOK)
	var listed struct {
		Windows []config.MaintenanceWindow `json:"windows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list error = %v", err)
	}
	_ = resp.Body.Close()
	if len(listed.Windows) != 1 || listed.Windows[0].ID != created.ID {
		t.Fatalf("unexpected list payload: %#v", listed)
	}

	updateBody := []byte(`{"title":"Backup window (updated)","active":false,"jobIDs":["job-1"],"strategy":"manual","timezone":"UTC"}`)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/maintenance/"+created.ID, bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var updated config.MaintenanceWindow
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated window error = %v", err)
	}
	_ = resp.Body.Close()
	if updated.Title != "Backup window (updated)" || updated.Active {
		t.Fatalf("update not applied: %#v", updated)
	}
	if updated.ID != created.ID {
		t.Fatalf("expected ID to be preserved across update, got %q want %q", updated.ID, created.ID)
	}

	resp = mustHTTPStatus(t, http.MethodDelete, srv.URL+"/api/maintenance/"+created.ID, nil, http.StatusOK)
	_ = resp.Body.Close()

	resp = mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/maintenance", nil, http.StatusOK)
	listed.Windows = nil
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list after delete error = %v", err)
	}
	_ = resp.Body.Close()
	if len(listed.Windows) != 0 {
		t.Fatalf("expected empty list after delete, got %#v", listed.Windows)
	}

	// Deleting again should 404.
	resp = mustHTTPStatus(t, http.MethodDelete, srv.URL+"/api/maintenance/"+created.ID, nil, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestMaintenanceListIncludesComputedOccurrences(t *testing.T) {
	jobs := []config.Job{{ID: "job-1", Name: "sonarr", Enabled: true, ContainerNameFilter: "sonarr"}}
	a, srv := newMaintenanceTestServer(t, jobs, nil)

	now := time.Now().UTC()
	past := now.Add(-2 * time.Hour)
	future := now.Add(2 * time.Hour)
	a.maintenanceWindows = []config.MaintenanceWindow{
		{
			ID: "m-past-range", Title: "Past-start manual", Active: true,
			Strategy: config.MaintenanceStrategyManual, Timezone: "UTC",
			JobIDs: []string{"job-1"}, EffectiveFrom: &past,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "m-future-range", Title: "Future-start manual", Active: true,
			Strategy: config.MaintenanceStrategyManual, Timezone: "UTC",
			JobIDs: []string{"job-1"}, EffectiveFrom: &future,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "m-open-ended", Title: "Open-ended manual", Active: true,
			Strategy: config.MaintenanceStrategyManual, Timezone: "UTC",
			JobIDs: []string{"job-1"},
			CreatedAt: now, UpdatedAt: now,
		},
	}

	resp := mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/maintenance", nil, http.StatusOK)
	var out struct {
		Windows []struct {
			ID             string     `json:"id"`
			LastOccurrence *time.Time `json:"lastOccurrence"`
			NextOccurrence *time.Time `json:"nextOccurrence"`
		} `json:"windows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	_ = resp.Body.Close()

	byID := map[string]struct {
		Last *time.Time
		Next *time.Time
	}{}
	for _, w := range out.Windows {
		byID[w.ID] = struct {
			Last *time.Time
			Next *time.Time
		}{w.LastOccurrence, w.NextOccurrence}
	}

	pastRange, ok := byID["m-past-range"]
	if !ok {
		t.Fatalf("expected past-range window present in %#v", out.Windows)
	}
	if pastRange.Last == nil || !pastRange.Last.Equal(past) {
		t.Errorf("expected lastOccurrence=%v for past-start manual window, got %v", past, pastRange.Last)
	}
	if pastRange.Next != nil {
		t.Errorf("expected no nextOccurrence for a manual window whose effective-from has already passed, got %v", pastRange.Next)
	}

	futureRange, ok := byID["m-future-range"]
	if !ok {
		t.Fatalf("expected future-range window present in %#v", out.Windows)
	}
	if futureRange.Last != nil {
		t.Errorf("expected no lastOccurrence for a manual window that hasn't started yet, got %v", futureRange.Last)
	}
	if futureRange.Next == nil || !futureRange.Next.Equal(future) {
		t.Errorf("expected nextOccurrence=%v for future-start manual window, got %v", future, futureRange.Next)
	}

	openEnded, ok := byID["m-open-ended"]
	if !ok {
		t.Fatalf("expected open-ended window present in %#v", out.Windows)
	}
	if openEnded.Last != nil {
		t.Errorf("expected no lastOccurrence for an open-ended manual window, got %v", openEnded.Last)
	}
	if openEnded.Next != nil {
		t.Errorf("expected no nextOccurrence for an open-ended manual window, got %v", openEnded.Next)
	}
}

func TestMaintenanceAPIRejectsInvalidWindow(t *testing.T) {
	_, srv := newMaintenanceTestServer(t, nil, nil)

	// No jobs/hosts targeted — normalizeMaintenanceWindow requires at least one.
	body := []byte(`{"title":"Bad window","active":true,"strategy":"manual"}`)
	resp := mustHTTPStatus(t, http.MethodPost, srv.URL+"/api/maintenance", body, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestMaintenanceActiveResolvesNamesAndWholeApp(t *testing.T) {
	jobs := []config.Job{
		{ID: "job-1", Name: "sonarr", Enabled: true, ContainerNameFilter: "sonarr"},
		{ID: "job-2", Name: "radarr", Enabled: true, ContainerNameFilter: "radarr"},
	}
	hosts := []config.DockerHostProfile{
		{ID: "local", Name: "Local Docker", BuiltIn: true, Enabled: true},
		{ID: "host-b", Name: "NAS", Enabled: true},
	}
	a, srv := newMaintenanceTestServer(t, jobs, hosts)

	now := time.Now().UTC()
	a.maintenanceWindows = []config.MaintenanceWindow{
		{
			ID: "m-active-mixed", Title: "Mixed window", Active: true,
			Strategy: config.MaintenanceStrategyManual,
			JobIDs:   []string{"job-1", "dangling-job"}, DockerHostIDs: []string{"host-b"},
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "m-active-local", Title: "Local window", Active: true,
			Strategy: config.MaintenanceStrategyManual, DockerHostIDs: []string{"local"},
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "m-inactive", Title: "Inactive window", Active: false,
			Strategy: config.MaintenanceStrategyManual, JobIDs: []string{"job-2"},
			CreatedAt: now, UpdatedAt: now,
		},
	}

	resp := mustHTTPStatus(t, http.MethodGet, srv.URL+"/api/maintenance/active", nil, http.StatusOK)
	var out struct {
		Windows []activeMaintenanceWindowView `json:"windows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode active error = %v", err)
	}
	_ = resp.Body.Close()

	if len(out.Windows) != 2 {
		t.Fatalf("expected 2 active windows (inactive one excluded), got %#v", out.Windows)
	}

	byID := map[string]activeMaintenanceWindowView{}
	for _, w := range out.Windows {
		byID[w.ID] = w
	}

	mixed, ok := byID["m-active-mixed"]
	if !ok {
		t.Fatalf("expected mixed window present in %#v", out.Windows)
	}
	if len(mixed.AffectsJobs) != 2 || mixed.AffectsJobs[0] != "sonarr" || mixed.AffectsJobs[1] != "dangling-job" {
		t.Errorf("expected resolved name for known job and raw ID fallback for dangling job, got %v", mixed.AffectsJobs)
	}
	if len(mixed.AffectsHosts) != 1 || mixed.AffectsHosts[0] != "NAS" {
		t.Errorf("expected resolved host name, got %v", mixed.AffectsHosts)
	}
	if mixed.WholeApp {
		t.Error("expected wholeApp=false for a window that does not target the local host")
	}

	local, ok := byID["m-active-local"]
	if !ok {
		t.Fatalf("expected local window present in %#v", out.Windows)
	}
	if !local.WholeApp {
		t.Error("expected wholeApp=true for a window targeting the local host")
	}
	if len(local.AffectsHosts) != 1 || local.AffectsHosts[0] != "Local Docker" {
		t.Errorf("expected resolved local host name, got %v", local.AffectsHosts)
	}
}
