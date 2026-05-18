package web

import (
	"encoding/json"
	"testing"
	"time"
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
