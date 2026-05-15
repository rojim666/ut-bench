package web

import (
	"encoding/json"
	"testing"
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
