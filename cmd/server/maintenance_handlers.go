package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"chowkidar/internal/config"
)

// maintenanceWindowView augments a stored window with computed, display-only
// last/next occurrence times — purely derived at request time (like
// activeMaintenanceWindowView), never persisted.
type maintenanceWindowView struct {
	config.MaintenanceWindow
	LastOccurrence *time.Time `json:"lastOccurrence,omitempty"`
	NextOccurrence *time.Time `json:"nextOccurrence,omitempty"`
}

func maintenanceWindowViews(windows []config.MaintenanceWindow, now time.Time) []maintenanceWindowView {
	views := make([]maintenanceWindowView, 0, len(windows))
	for _, win := range windows {
		view := maintenanceWindowView{MaintenanceWindow: win}
		if last, ok := win.LastOccurrence(now); ok {
			view.LastOccurrence = &last
		}
		if next, ok := win.NextOccurrence(now); ok {
			view.NextOccurrence = &next
		}
		views = append(views, view)
	}
	return views
}

// handleMaintenance handles GET (list) and POST (create) for /api/maintenance.
func (a *app) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"windows": maintenanceWindowViews(a.getMaintenanceWindows(), time.Now())})
	case http.MethodPost:
		var in config.MaintenanceWindow
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		windows := a.getMaintenanceWindows()
		next, saved, err := config.UpsertMaintenanceWindow(windows, in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := a.persistMaintenanceWindows(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleMaintenanceByID handles PUT (update) and DELETE for /api/maintenance/{id}.
func (a *app) handleMaintenanceByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/maintenance/")
	id = strings.TrimSpace(id)
	if id == "" || id == "active" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "maintenance window id is required"})
		return
	}

	switch r.Method {
	case http.MethodPut:
		var in config.MaintenanceWindow
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		in.ID = id

		windows := a.getMaintenanceWindows()
		next, saved, err := config.UpsertMaintenanceWindow(windows, in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := a.persistMaintenanceWindows(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		windows := a.getMaintenanceWindows()
		next, removed := config.DeleteMaintenanceWindow(windows, id)
		if !removed {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "maintenance window not found"})
			return
		}
		if err := a.persistMaintenanceWindows(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// activeMaintenanceWindowView is the dashboard-banner-friendly resolution of
// an active window's raw job/host IDs into display names.
type activeMaintenanceWindowView struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Strategy     string   `json:"strategy"`
	AffectsJobs  []string `json:"affectsJobs"`
	AffectsHosts []string `json:"affectsHosts"`
	WholeApp     bool     `json:"wholeApp"`
}

// handleMaintenanceActive powers the dashboard banner — it resolves currently
// active windows' job/host IDs to display names (falling back to the raw ID
// for dangling references) so the frontend can render a summary without
// needing the full job/host lists.
func (a *app) handleMaintenanceActive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	active := config.ActiveMaintenanceWindows(a.getMaintenanceWindows(), time.Now())

	jobNames := make(map[string]string)
	for _, j := range a.getJobs() {
		jobNames[j.ID] = j.Name
	}
	// The synthetic built-in "local" profile isn't stored in a.dockerHosts
	// (handleDockerHosts prepends it on the fly), so seed its display name here —
	// otherwise a window targeting "local" would render the raw ID instead of
	// "Local Docker" in the dashboard banner.
	hostNames := map[string]string{"local": builtInDockerHostProfile(a.getConfig().DockerSocketPath).Name}
	for _, h := range a.getDockerHostProfiles() {
		hostNames[h.ID] = h.Name
	}

	views := make([]activeMaintenanceWindowView, 0, len(active))
	for _, win := range active {
		view := activeMaintenanceWindowView{
			ID:          win.ID,
			Title:       win.Title,
			Description: win.Description,
			Strategy:    win.Strategy,
		}
		for _, id := range win.JobIDs {
			if name, ok := jobNames[id]; ok {
				view.AffectsJobs = append(view.AffectsJobs, name)
			} else {
				view.AffectsJobs = append(view.AffectsJobs, id)
			}
		}
		for _, id := range win.DockerHostIDs {
			if id == "local" {
				view.WholeApp = true
			}
			if name, ok := hostNames[id]; ok {
				view.AffectsHosts = append(view.AffectsHosts, name)
			} else {
				view.AffectsHosts = append(view.AffectsHosts, id)
			}
		}
		views = append(views, view)
	}

	writeJSON(w, http.StatusOK, map[string]any{"windows": views})
}
