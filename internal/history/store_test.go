package history

import (
	"testing"
	"time"
)

func TestStoreAppendAndListNewestFirst(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	first := Entry{Timestamp: time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC), ContainerID: "1", ContainerName: "one", Reason: "unhealthy", Action: "restart", Attempt: 1, Status: "failed"}
	second := Entry{Timestamp: time.Date(2026, 5, 25, 10, 1, 0, 0, time.UTC), ContainerID: "2", ContainerName: "two", Reason: "unhealthy", Action: "restart", Attempt: 2, Status: "success"}
	if err := store.Append(first); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if err := store.Append(second); err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}

	entries, err := store.List(10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ContainerID != "2" || entries[1].ContainerID != "1" {
		t.Fatalf("entries not returned newest-first: %#v", entries)
	}
}

func TestStoreListPagePaginationAndFilter(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	// Append 5 entries for "alpha" and 3 for "beta".
	for i := 1; i <= 5; i++ {
		_ = store.Append(Entry{
			Timestamp:     time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC),
			ContainerName: "alpha",
			Status:        "failed",
		})
	}
	for i := 1; i <= 3; i++ {
		_ = store.Append(Entry{
			Timestamp:     time.Date(2026, 1, 1, 1, i, 0, 0, time.UTC),
			ContainerName: "beta",
			Status:        "success",
		})
	}

	// All entries, page 1 of 2 (limit=5).
	page1, total, err := store.ListPage(ListOptions{Limit: 5, Offset: 0})
	if err != nil {
		t.Fatalf("ListPage page1 error = %v", err)
	}
	if total != 8 {
		t.Errorf("total: got %d, want 8", total)
	}
	if len(page1) != 5 {
		t.Errorf("page1 len: got %d, want 5", len(page1))
	}

	// Page 2.
	page2, _, err := store.ListPage(ListOptions{Limit: 5, Offset: 5})
	if err != nil {
		t.Fatalf("ListPage page2 error = %v", err)
	}
	if len(page2) != 3 {
		t.Errorf("page2 len: got %d, want 3", len(page2))
	}

	// Filter by service "alpha".
	alphaPage, alphaTotal, err := store.ListPage(ListOptions{Limit: 10, Service: "alpha"})
	if err != nil {
		t.Fatalf("ListPage alpha error = %v", err)
	}
	if alphaTotal != 5 {
		t.Errorf("alpha total: got %d, want 5", alphaTotal)
	}
	if len(alphaPage) != 5 {
		t.Errorf("alpha page len: got %d, want 5", len(alphaPage))
	}
	for _, e := range alphaPage {
		if e.ContainerName != "alpha" {
			t.Errorf("expected alpha, got %q", e.ContainerName)
		}
	}

	// Offset beyond total returns empty slice with correct total.
	empty, emptyTotal, err := store.ListPage(ListOptions{Limit: 5, Offset: 100})
	if err != nil {
		t.Fatalf("ListPage offset>total error = %v", err)
	}
	if emptyTotal != 8 {
		t.Errorf("emptyTotal: got %d, want 8", emptyTotal)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 entries beyond total, got %d", len(empty))
	}
}
