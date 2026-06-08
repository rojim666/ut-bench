package dataset

import (
	"reflect"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestNormalizeBenchmarkProfileSupportsCustom(t *testing.T) {
	tests := map[string]string{
		"small":   "small",
		"Medium":  "medium",
		" large ": "large",
		"custom":  "custom",
		"CUSTOM":  "custom",
		"other":   "",
	}

	for input, want := range tests {
		if got := NormalizeBenchmarkProfile(input); got != want {
			t.Fatalf("NormalizeBenchmarkProfile(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestApplyBenchmarkProfileKeepsCustomSelection(t *testing.T) {
	original := contracts.RunSpec{
		BenchmarkProfile: "custom",
		Languages:        []string{"python", "java"},
		DatasetClasses:   []string{"repo_level"},
		DatasetScenario:  "boundary",
		DatasetProject:   "demo",
		DatasetLevel:     "l2,l3",
		DatasetManifest:  "configs/custom.json",
		MaxSamples:       12,
	}

	got := ApplyBenchmarkProfile(original)
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("custom profile should keep user selection\n got: %#v\nwant: %#v", got, original)
	}
}

func TestApplyBenchmarkProfileLocksFixedSelection(t *testing.T) {
	spec := contracts.RunSpec{
		BenchmarkProfile: "medium",
		Languages:        []string{"python"},
		DatasetClasses:   []string{"repo_level"},
		DatasetScenario:  "boundary",
		DatasetProject:   "demo",
		DatasetLevel:     "l3",
		DatasetManifest:  "configs/custom.json",
		MaxSamples:       999,
	}

	got := ApplyBenchmarkProfile(spec)
	if got.BenchmarkProfile != "medium" {
		t.Fatalf("unexpected benchmark profile: %s", got.BenchmarkProfile)
	}
	if got.DatasetManifest != "" {
		t.Fatalf("fixed profile should clear dataset manifest, got %q", got.DatasetManifest)
	}
	if got.DatasetScenario != "" {
		t.Fatalf("fixed profile should clear dataset scenario, got %q", got.DatasetScenario)
	}
	if got.DatasetProject != "" {
		t.Fatalf("fixed profile should clear dataset project, got %q", got.DatasetProject)
	}
	if got.DatasetLevel != "l1,l2" {
		t.Fatalf("fixed profile should force levels, got %q", got.DatasetLevel)
	}
	if got.MaxSamples != 30 {
		t.Fatalf("fixed profile should cap max samples, got %d", got.MaxSamples)
	}
	if !reflect.DeepEqual(got.Languages, []string{"python", "go", "java", "cpp"}) {
		t.Fatalf("fixed profile should force languages, got %#v", got.Languages)
	}
	if !reflect.DeepEqual(got.DatasetClasses, []string{"self_contained"}) {
		t.Fatalf("fixed profile should force classes, got %#v", got.DatasetClasses)
	}
}
