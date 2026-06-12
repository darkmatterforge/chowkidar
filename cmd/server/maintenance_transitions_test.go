package main

import (
	"testing"
	"time"

	"chowkidar/internal/config"
)

func TestCheckMaintenanceTransitions(t *testing.T) {
	a := &app{}
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	win := config.MaintenanceWindow{ID: "m1", Title: "Backup window"}

	// Edge 1: window becomes active — records a single "started" transition.
	a.checkMaintenanceTransitions([]config.MaintenanceWindow{win}, base)
	if got := transitionsOfKind(a.maintenanceTransitions, "started"); len(got) != 1 || got[0].WindowID != "m1" || got[0].Title != "Backup window" {
		t.Fatalf("after start edge: started=%#v, want one entry for m1/Backup window", got)
	}
	if got := transitionsOfKind(a.maintenanceTransitions, "ended"); len(got) != 0 {
		t.Fatalf("after start edge: ended=%#v, want none", got)
	}

	// Steady state: still active on the next cycle — no new transition recorded.
	a.checkMaintenanceTransitions([]config.MaintenanceWindow{win}, base.Add(time.Minute))
	if got := transitionsOfKind(a.maintenanceTransitions, "started"); len(got) != 1 {
		t.Fatalf("steady state while active: started=%#v, want still exactly one (no duplicate)", got)
	}

	// Edge 2: window becomes inactive — records a single "ended" transition,
	// carrying the title even though the window is no longer in the active list.
	endedAt := base.Add(2 * time.Hour)
	a.checkMaintenanceTransitions(nil, endedAt)
	ended := transitionsOfKind(a.maintenanceTransitions, "ended")
	if len(ended) != 1 || ended[0].WindowID != "m1" || ended[0].Title != "Backup window" || !ended[0].At.Equal(endedAt) {
		t.Fatalf("after end edge: ended=%#v, want one entry for m1/Backup window at %v", ended, endedAt)
	}

	// Steady state: still inactive — no new transition recorded.
	a.checkMaintenanceTransitions(nil, base.Add(3*time.Hour))
	if got := transitionsOfKind(a.maintenanceTransitions, "ended"); len(got) != 1 {
		t.Fatalf("steady state while inactive: ended=%#v, want still exactly one (no duplicate)", got)
	}

	// Re-activation — records a fresh "started" transition (distinct from the first).
	restartAt := base.Add(4 * time.Hour)
	a.checkMaintenanceTransitions([]config.MaintenanceWindow{win}, restartAt)
	started := transitionsOfKind(a.maintenanceTransitions, "started")
	if len(started) != 2 {
		t.Fatalf("after re-activation: started=%#v, want two entries (initial + re-activation)", started)
	}
	if !started[1].At.Equal(restartAt) {
		t.Fatalf("re-activation transition At = %v, want %v", started[1].At, restartAt)
	}
}

func TestCheckMaintenanceTransitionsPrunesStaleEntries(t *testing.T) {
	a := &app{}
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	win := config.MaintenanceWindow{ID: "m1", Title: "Old window"}

	// Record a started transition far enough in the past to fall outside the
	// retention window once a later edge triggers a prune.
	a.checkMaintenanceTransitions([]config.MaintenanceWindow{win}, base)
	if len(a.maintenanceTransitions) != 1 {
		t.Fatalf("expected one transition recorded, got %d", len(a.maintenanceTransitions))
	}

	// A fresh edge more than the retention window later should prune the stale
	// entry while still recording the new one.
	later := base.Add(maintenanceTransitionRetention + time.Hour)
	a.checkMaintenanceTransitions(nil, later)

	for _, tr := range a.maintenanceTransitions {
		if tr.At.Equal(base) {
			t.Fatalf("expected stale transition at %v to be pruned, but it's still present: %#v", base, a.maintenanceTransitions)
		}
	}
	ended := transitionsOfKind(a.maintenanceTransitions, "ended")
	if len(ended) != 1 || !ended[0].At.Equal(later) {
		t.Fatalf("expected the new ended transition at %v to remain, got %#v", later, a.maintenanceTransitions)
	}
}

func transitionsOfKind(transitions []maintenanceTransition, kind string) []maintenanceTransition {
	var out []maintenanceTransition
	for _, tr := range transitions {
		if tr.Kind == kind {
			out = append(out, tr)
		}
	}
	return out
}
