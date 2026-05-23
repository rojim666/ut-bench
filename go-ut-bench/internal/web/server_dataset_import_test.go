package web

import "testing"

func TestNormalizeDatasetPackagePathAcceptsRepoLevelProject(t *testing.T) {
	rel, ok := normalizeDatasetPackagePath("datasets/go/go_code_files_repo_level/dogfood/go-ut-bench/internal/runner/api.go")
	if !ok {
		t.Fatal("expected repo_level project file to be accepted")
	}
	want := "go/repo_level/dogfood/go-ut-bench/internal/runner/api.go"
	if rel != want {
		t.Fatalf("expected %q, got %q", want, rel)
	}
}

func TestNormalizeDatasetPackagePathAcceptsRepoLevelProjectMeta(t *testing.T) {
	rel, ok := normalizeDatasetPackagePath("go/go_code_files_repo_level/dogfood/go-ut-bench/internal/runner/api.meta.json")
	if !ok {
		t.Fatal("expected repo_level project meta file to be accepted")
	}
	want := "go/repo_level/dogfood/go-ut-bench/internal/runner/api.meta.json"
	if rel != want {
		t.Fatalf("expected %q, got %q", want, rel)
	}
}

func TestNormalizeDatasetPackagePathStillAcceptsLegacyRepoWorkspace(t *testing.T) {
	rel, ok := normalizeDatasetPackagePath("python/python_code_files_repo_level/simple_function/workspace/pkg/mod.py")
	if !ok {
		t.Fatal("expected legacy repo_level workspace file to be accepted")
	}
	want := "python/repo_level/simple_function/workspace/pkg/mod.py"
	if rel != want {
		t.Fatalf("expected %q, got %q", want, rel)
	}
}

func TestNormalizeDatasetPackagePathAcceptsShortSelfContainedName(t *testing.T) {
	rel, ok := normalizeDatasetPackagePath("datasets/python/self_contained/simple_function/041.py")
	if !ok {
		t.Fatal("expected short self_contained sample file to be accepted")
	}
	want := "python/self_contained/simple_function/041.py"
	if rel != want {
		t.Fatalf("expected %q, got %q", want, rel)
	}
}

func TestNormalizeDatasetPackagePathNormalizesLegacySelfContainedName(t *testing.T) {
	rel, ok := normalizeDatasetPackagePath("datasets/python/python_code_files_self_contained/simple_function/simple_function_041.py")
	if !ok {
		t.Fatal("expected legacy self_contained sample file to be accepted")
	}
	want := "python/self_contained/simple_function/041.py"
	if rel != want {
		t.Fatalf("expected %q, got %q", want, rel)
	}
}
