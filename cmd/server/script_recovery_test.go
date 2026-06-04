package main

import (
	"context"
	"strings"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"

	"chowkidar/internal/config"
	"chowkidar/internal/history"
	"chowkidar/internal/notify"
)

// ── executeRunScript ──────────────────────────────────────────────────────────

func TestScriptRecovery_InlineScriptOutputCaptured(t *testing.T) {
	a := &app{cfg: config.Config{ActionTimeoutSeconds: 10}}
	c := containertypes.Summary{ID: "s1", Names: []string{"/web"}}
	ctx := context.Background()

	out, err := a.executeRunScript(ctx, `#!/bin/sh
echo "healing: web"
echo "container id: $1"`, config.Config{ActionTimeoutSeconds: 10}, c, "web", false)

	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if !strings.Contains(out, "healing: web") {
		t.Fatalf("expected 'healing: web' in output, got: %q", out)
	}
	if !strings.Contains(out, "container id: s1") {
		t.Fatalf("expected container id in output, got: %q", out)
	}
}

func TestScriptRecovery_FailedScriptOutputIncludedInError(t *testing.T) {
	a := &app{cfg: config.Config{ActionTimeoutSeconds: 10}}
	c := containertypes.Summary{ID: "s2", Names: []string{"/db"}}
	ctx := context.Background()

	out, err := a.executeRunScript(ctx, `#!/bin/sh
echo "attempting restart"
exit 1`, config.Config{ActionTimeoutSeconds: 10}, c, "db", false)

	if err == nil {
		t.Fatal("expected script to fail (exit 1)")
	}
	// Output should be captured even on failure
	if !strings.Contains(out, "attempting restart") {
		t.Fatalf("expected output captured on failure, got: %q (err: %v)", out, err)
	}
}

func TestScriptRecovery_OutputStoredInHistoryEntry(t *testing.T) {
	dir := t.TempDir()
	store, _ := history.NewStore(dir)
	fake := &fakeDockerClient{}
	a := &app{
		cfg:                  config.Config{ActionTimeoutSeconds: 10},
		docker:               fake,
		history:              store,
		notifier:             notify.New(""),
		lastNotified:         make(map[string]time.Time),
		actionCycle:          make(map[string]int),
		retriesExhausted:     make(map[string]bool),
		postActionDeadline:   make(map[string]time.Time),
		activeJobs:           make(map[string]bool),
		lastJobNotifications: make(map[string][]string),
	}

	job := actionJob{
		app:       a,
		container: containertypes.Summary{ID: "t1", Names: []string{"/cache"}},
		reason:    "unhealthy",
		action:    "run-script",
		script:    "#!/bin/sh\necho 'cache healed'\n",
	}
	job.Run(context.Background())

	entries, err := store.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Status == "success" && e.ContainerName == "cache" {
			found = true
			if !strings.Contains(e.Output, "cache healed") {
				t.Fatalf("expected 'cache healed' in history output, got: %q", e.Output)
			}
		}
	}
	if !found {
		t.Fatal("no success entry found for 'cache' after script run")
	}
}

func TestScriptRecovery_MultiLineOutputPreserved(t *testing.T) {
	a := &app{cfg: config.Config{ActionTimeoutSeconds: 10}}
	c := containertypes.Summary{ID: "m1", Names: []string{"/app"}}
	ctx := context.Background()

	out, err := a.executeRunScript(ctx, `#!/bin/sh
echo "line 1: checking container"
echo "line 2: sending alert"
echo "line 3: restarting service"`, config.Config{ActionTimeoutSeconds: 10}, c, "app", false)

	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	for _, expected := range []string{"line 1", "line 2", "line 3"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in multi-line output, got: %q", expected, out)
		}
	}
}

func TestScriptRecovery_EmptyScriptOutputIsEmpty(t *testing.T) {
	a := &app{cfg: config.Config{ActionTimeoutSeconds: 10}}
	c := containertypes.Summary{ID: "e1", Names: []string{"/quiet"}}
	ctx := context.Background()

	out, err := a.executeRunScript(ctx, "#!/bin/sh\n# no output\n", config.Config{ActionTimeoutSeconds: 10}, c, "quiet", false)

	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output for silent script, got: %q", out)
	}
}
