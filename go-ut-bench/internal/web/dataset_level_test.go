package web

import (
	"os"
	"path/filepath"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestNormalizeCustomDatasetLevelDropsUnavailableLevel(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "datasets")
	writeDatasetFile(t, filepath.Join(datasetRoot, "l1", "go", "self_contained", "boundary", "boundary_000.go"))

	spec := normalizeCustomDatasetLevel(contracts.RunSpec{
		BenchmarkProfile: "custom",
		DatasetRoot:      datasetRoot,
		DatasetLevel:     "l1",
		Languages:        []string{"java", "cpp"},
		DatasetClasses:   []string{"self_contained"},
	})

	if spec.DatasetLevel != "" {
		t.Fatalf("expected unavailable l1 to be cleared, got %q", spec.DatasetLevel)
	}
}

func TestNormalizeCustomDatasetLevelKeepsAvailableLevels(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "datasets")
	writeDatasetFile(t, filepath.Join(datasetRoot, "l1", "go", "self_contained", "boundary", "boundary_000.go"))
	writeDatasetFile(t, filepath.Join(datasetRoot, "l2", "java", "self_contained", "boundary", "boundary_000.java"))

	spec := normalizeCustomDatasetLevel(contracts.RunSpec{
		BenchmarkProfile: "custom",
		DatasetRoot:      datasetRoot,
		DatasetLevel:     "l1,l2",
		Languages:        []string{"java"},
		DatasetClasses:   []string{"self_contained"},
	})

	if spec.DatasetLevel != "l2" {
		t.Fatalf("expected only l2 to remain, got %q", spec.DatasetLevel)
	}
}

func TestNormalizeCustomDatasetLevelLeavesFixedProfileUnchanged(t *testing.T) {
	spec := normalizeCustomDatasetLevel(contracts.RunSpec{
		BenchmarkProfile: "small",
		DatasetRoot:      t.TempDir(),
		DatasetLevel:     "l1",
		Languages:        []string{"java"},
		DatasetClasses:   []string{"self_contained"},
	})

	if spec.DatasetLevel != "l1" {
		t.Fatalf("expected fixed profile level to remain unchanged, got %q", spec.DatasetLevel)
	}
}

func writeDatasetFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
