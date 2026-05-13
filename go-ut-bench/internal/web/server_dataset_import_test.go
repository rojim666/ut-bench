package web

import "testing"

func TestNormalizeDatasetPackagePathAcceptsRepoLevelProject(t *testing.T) {
	rel, ok := normalizeDatasetPackagePath("datasets/go/go_code_files_repo_level/dogfood/go-ut-bench/internal/runner/api.go")
	if !ok {
		t.Fatal("expected repo_level project file to be accepted")
	}
	want := "go/go_code_files_repo_level/dogfood/go-ut-bench/internal/runner/api.go"
	if rel != want {
		t.Fatalf("expected %q, got %q", want, rel)
	}
}

func TestNormalizeDatasetPackagePathAcceptsRepoLevelProjectMeta(t *testing.T) {
	rel, ok := normalizeDatasetPackagePath("go/go_code_files_repo_level/dogfood/go-ut-bench/internal/runner/api.meta.json")
	if !ok {
		t.Fatal("expected repo_level project meta file to be accepted")
	}
	want := "go/go_code_files_repo_level/dogfood/go-ut-bench/internal/runner/api.meta.json"
	if rel != want {
		t.Fatalf("expected %q, got %q", want, rel)
	}
}

func TestNormalizeDatasetPackagePathStillAcceptsLegacyRepoWorkspace(t *testing.T) {
	rel, ok := normalizeDatasetPackagePath("python/python_code_files_repo_level/simple_function/workspace/pkg/mod.py")
	if !ok {
		t.Fatal("expected legacy repo_level workspace file to be accepted")
	}
	want := "python/python_code_files_repo_level/simple_function/workspace/pkg/mod.py"
	if rel != want {
		t.Fatalf("expected %q, got %q", want, rel)
	}
}
