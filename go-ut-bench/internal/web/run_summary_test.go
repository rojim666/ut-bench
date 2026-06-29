package web

import (
	"encoding/json"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"
)

func TestDecodeDiskRunSummaryKeepsHistoricalMissingReuseGeneratedFalse(t *testing.T) {
	raw := []byte(`{
		"run_id":"run-1",
		"spec":{
			"run_id":"run-1",
			"models":["deepseek"],
			"languages":["go"],
			"dataset_classes":["repo_level"]
		}
	}`)

	summary, err := decodeDiskRunSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Spec.ReuseGenerated {
		t.Fatal("expected missing reuse_generated from historical summaries to remain false")
	}
}

func TestRunSpecSerializesReuseFlagsExplicitly(t *testing.T) {
	summary, err := decodeDiskRunSummary([]byte(`{
		"run_id":"run-1",
		"spec":{
			"run_id":"run-1",
			"reuse_generated":false,
			"reuse_evaluation":false
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(summary.Spec)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["reuse_generated"]; !ok {
		t.Fatalf("expected reuse_generated to be serialized explicitly: %s", raw)
	}
	if _, ok := fields["reuse_evaluation"]; !ok {
		t.Fatalf("expected reuse_evaluation to be serialized explicitly: %s", raw)
	}
}

func TestDiskRunSummaryUsesSpecCreatedAsStartAndSummaryTimeAsEnd(t *testing.T) {
	raw := []byte(`{
		"run_id":"run-1",
		"created_at_utc":"2026-05-15T10:05:00Z",
		"spec":{
			"run_id":"run-1",
			"created_at_utc":"2026-05-15T10:00:00Z"
		}
	}`)

	summary, err := decodeDiskRunSummary(raw)
	if err != nil {
		t.Fatal(err)
	}

	startedAt := diskRunStartedAt(summary)
	if got, want := startedAt.Format(time.RFC3339), "2026-05-15T10:00:00Z"; got != want {
		t.Fatalf("started_at = %s, want %s", got, want)
	}
	endedAt := diskRunEndedAt(summary)
	if endedAt == nil {
		t.Fatal("expected ended_at to be recovered")
	}
	if got, want := endedAt.Format(time.RFC3339), "2026-05-15T10:05:00Z"; got != want {
		t.Fatalf("ended_at = %s, want %s", got, want)
	}
}

func TestDiskRunSummaryPrefersCompletedAtForEnd(t *testing.T) {
	raw := []byte(`{
		"run_id":"run-1",
		"created_at_utc":"2026-05-15T10:05:00Z",
		"completed_at_utc":"2026-05-15T10:06:00Z",
		"spec":{"run_id":"run-1"}
	}`)

	summary, err := decodeDiskRunSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	endedAt := diskRunEndedAt(summary)
	if endedAt == nil {
		t.Fatal("expected ended_at to be recovered")
	}
	if got, want := endedAt.Format(time.RFC3339), "2026-05-15T10:06:00Z"; got != want {
		t.Fatalf("ended_at = %s, want %s", got, want)
	}
}

func TestReconcileRunEntryWithDiskMarksStaleRunningCompleted(t *testing.T) {
	startedAt := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(5 * time.Minute)
	entry := &RunEntry{
		RunID:     "run-1",
		Status:    StatusRunning,
		StartedAt: startedAt,
		Spec:      contracts.RunSpec{RunID: "run-1"},
		Done:      make(chan struct{}),
	}
	server := &Server{}

	item := server.reconcileRunEntryWithDisk(entry, runSummaryItem{
		RunID:     "run-1",
		Status:    StatusCompleted,
		StartedAt: startedAt,
		EndedAt:   &completedAt,
		Spec:      contracts.RunSpec{RunID: "run-1", Models: []string{"deepseek"}},
	}, true)

	if item.Status != StatusCompleted {
		t.Fatalf("summary status = %s, want completed", item.Status)
	}
	entry.mu.RLock()
	defer entry.mu.RUnlock()
	if entry.Status != StatusCompleted {
		t.Fatalf("entry status = %s, want completed", entry.Status)
	}
	if entry.EndedAt == nil || !entry.EndedAt.Equal(completedAt) {
		t.Fatalf("entry ended_at = %v, want %v", entry.EndedAt, completedAt)
	}
	if got := entry.Spec.Models; len(got) != 1 || got[0] != "deepseek" {
		t.Fatalf("entry spec models = %+v", got)
	}
}

func TestReconcileRunEntryWithDiskDoesNotOverrideNewerRunWithOldSummary(t *testing.T) {
	startedAt := time.Date(2026, 5, 15, 10, 10, 0, 0, time.UTC)
	oldCompletedAt := startedAt.Add(-time.Minute)
	entry := &RunEntry{
		RunID:     "run-1",
		Status:    StatusRunning,
		StartedAt: startedAt,
		Spec:      contracts.RunSpec{RunID: "run-1"},
		Done:      make(chan struct{}),
	}
	server := &Server{}

	item := server.reconcileRunEntryWithDisk(entry, runSummaryItem{
		RunID:     "run-1",
		Status:    StatusCompleted,
		StartedAt: startedAt.Add(-10 * time.Minute),
		EndedAt:   &oldCompletedAt,
		Spec:      contracts.RunSpec{RunID: "run-1"},
	}, true)

	if item.Status != StatusRunning {
		t.Fatalf("summary status = %s, want running", item.Status)
	}
	entry.mu.RLock()
	defer entry.mu.RUnlock()
	if entry.Status != StatusRunning {
		t.Fatalf("entry status = %s, want running", entry.Status)
	}
}
