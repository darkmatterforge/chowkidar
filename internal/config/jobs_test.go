package config

import (
	"os"
	"testing"
	"time"
)

func TestUpsertJobValidationAndUpdate(t *testing.T) {
	jobs, saved, err := UpsertJob(nil, Job{Name: "sonarr", Action: "restart", ContainerNameFilter: "sonarr"})
	if err != nil {
		t.Fatalf("UpsertJob() error = %v", err)
	}
	if len(jobs) != 1 || saved.ID == "" {
		t.Fatalf("unexpected upsert result: jobs=%#v saved=%#v", jobs, saved)
	}
	createdAt := saved.CreatedAt
	time.Sleep(1 * time.Millisecond)

	nextJobs, updated, err := UpsertJob(jobs, Job{ID: saved.ID, Name: "sonarr", Action: "restart", ContainerNameFilter: "sonarr", StartExited: true})
	if err != nil {
		t.Fatalf("UpsertJob update error = %v", err)
	}
	if len(nextJobs) != 1 {
		t.Fatalf("expected one job after update, got %d", len(nextJobs))
	}
	if !updated.CreatedAt.Equal(createdAt) {
		t.Fatalf("createdAt changed: before=%v after=%v", createdAt, updated.CreatedAt)
	}
	if updated.Action != "restart" || !updated.StartExited {
		t.Fatalf("updated job not applied: %#v", updated)
	}
}

func TestUpsertJobRequiresFilter(t *testing.T) {
	_, _, err := UpsertJob(nil, Job{Name: "invalid", Action: "restart"})
	if err == nil {
		t.Fatal("expected validation error for missing filter")
	}
}

func TestMigrateJobsV0ToV1RewritesRulePrefix(t *testing.T) {
	input := []Job{
		{ID: "rule-111", Name: "a", ContainerNameFilter: "a"},
		{ID: "rule-222", Name: "b", ContainerNameFilter: "b"},
		{ID: "job-333", Name: "c", ContainerNameFilter: "c"},
	}
	migrated, result := migrateJobs(0, input)
	if !migrated {
		t.Fatal("expected migrated=true")
	}
	if result[0].ID != "job-111" || result[1].ID != "job-222" {
		t.Fatalf("unexpected IDs after migration: %v %v", result[0].ID, result[1].ID)
	}
	if result[2].ID != "job-333" {
		t.Fatalf("already-prefixed ID should be unchanged: %v", result[2].ID)
	}
}

func TestMigrateJobsCurrentVersionNoOp(t *testing.T) {
	input := []Job{
		{ID: "job-111", Name: "a", ContainerNameFilter: "a"},
	}
	migrated, _ := migrateJobs(jobsConfigVersion, input)
	if migrated {
		t.Fatal("expected no migration for current version")
	}
}

func TestLoadJobsMigratesRulePrefixOnDisk(t *testing.T) {
	dir := t.TempDir()
	// Write a v0 jobs.yaml with rule- prefixed IDs (no version field).
	content := "jobs:\n  - id: rule-999\n    name: test\n    enabled: true\n    action: restart\n    containerNameFilter: test\n    createdAt: 2026-01-01T00:00:00Z\n    updatedAt: 2026-01-01T00:00:00Z\n"
	if err := writeFile(dir+"/jobs.yaml", content); err != nil {
		t.Fatal(err)
	}

	jobs, err := LoadJobs(dir)
	if err != nil {
		t.Fatalf("LoadJobs error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-999" {
		t.Fatalf("expected migrated ID job-999, got %v", jobs)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
