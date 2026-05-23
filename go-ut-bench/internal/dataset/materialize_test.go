package dataset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeLevelsDerivesFromBenchmarkProfiles(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "datasets")
	configRoot := filepath.Join(root, "configs")
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(datasetRoot, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("python/python_code_files_self_contained/boundary/boundary_000.py", "def f():\n    return 0\n")
	write("python/python_code_files_self_contained/boundary/boundary_001.py", "def f():\n    return 1\n")
	write("python/python_code_files_self_contained/boundary/boundary_002.py", "def f():\n    return 2\n")

	small := `{"level":"l1","samples":[{"id":"boundary_000","language":"python","category":"self_contained","scenario":"boundary","path":"python/python_code_files_self_contained/boundary/boundary_000.py"}]}`
	medium := `{"level":"l1,l2","samples":[{"id":"boundary_000","language":"python","category":"self_contained","scenario":"boundary","path":"python/python_code_files_self_contained/boundary/boundary_000.py"},{"id":"boundary_001","language":"python","category":"self_contained","scenario":"boundary","path":"python/python_code_files_self_contained/boundary/boundary_001.py"}]}`
	large := `{"level":"l1,l2,l3","samples":[{"id":"boundary_000","language":"python","category":"self_contained","scenario":"boundary","path":"python/python_code_files_self_contained/boundary/boundary_000.py"},{"id":"boundary_001","language":"python","category":"self_contained","scenario":"boundary","path":"python/python_code_files_self_contained/boundary/boundary_001.py"},{"id":"boundary_002","language":"python","category":"self_contained","scenario":"boundary","path":"python/python_code_files_self_contained/boundary/boundary_002.py"}]}`
	for name, content := range map[string]string{
		"dataset_small.json":  small,
		"dataset_medium.json": medium,
		"dataset_large.json":  large,
	} {
		if err := os.WriteFile(filepath.Join(configRoot, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService()
	summary, err := svc.MaterializeLevels(MaterializeLevelsOptions{
		DatasetRoot: datasetRoot,
		ConfigRoot:  configRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Levels) != 3 {
		t.Fatalf("expected 3 levels, got %d", len(summary.Levels))
	}

	for level, expected := range map[string]string{
		"l1": filepath.Join(datasetRoot, "l1", "python", "python_code_files_self_contained", "boundary", "boundary_000.py"),
		"l2": filepath.Join(datasetRoot, "l2", "python", "python_code_files_self_contained", "boundary", "boundary_001.py"),
		"l3": filepath.Join(datasetRoot, "l3", "python", "python_code_files_self_contained", "boundary", "boundary_002.py"),
	} {
		if _, err := os.Stat(expected); err != nil {
			t.Fatalf("expected %s file to be materialized: %v", level, err)
		}
	}

	if _, err := os.Stat(filepath.Join(datasetRoot, "l1", "python", "python_code_files_self_contained", "boundary", "boundary_001.py")); err == nil {
		t.Fatalf("expected l1 to remain incremental, but found boundary_001.py")
	}

	if _, err := svc.MaterializeLevels(MaterializeLevelsOptions{
		DatasetRoot: datasetRoot,
		ConfigRoot:  configRoot,
		Check:       true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeLevelsCopiesRepoLevelWorkspace(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "datasets")
	configRoot := filepath.Join(root, "configs")
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	projectRoot := filepath.Join(datasetRoot, "go", "go_code_files_repo_level", "oss", "demo")
	for _, dir := range []string{
		filepath.Join(projectRoot, "pkg"),
		filepath.Join(projectRoot, ".utbench"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(projectRoot, "go.mod"), "module example.com/demo\n")
	write(filepath.Join(projectRoot, "pkg", "demo.go"), "package pkg\n\nfunc Demo() int { return 1 }\n")
	write(filepath.Join(projectRoot, ".utbench", "project_profile.json"), `{"name":"demo"}`)

	manifest := `{"level":"l1","samples":[{"id":"pkg_demo","language":"go","category":"repo_level","scenario":"oss","path":"go/go_code_files_repo_level/oss/demo/pkg/demo.go"}]}`
	if err := os.WriteFile(filepath.Join(configRoot, "dataset_l1.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"l2", "l3"} {
		if err := os.WriteFile(filepath.Join(configRoot, "dataset_"+level+".json"), []byte(`{"level":"`+level+`","samples":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService()
	if _, err := svc.MaterializeLevels(MaterializeLevelsOptions{
		DatasetRoot: datasetRoot,
		ConfigRoot:  configRoot,
		Levels:      []string{"l1"},
	}); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"go/go_code_files_repo_level/oss/demo/go.mod",
		"go/go_code_files_repo_level/oss/demo/pkg/demo.go",
		"go/go_code_files_repo_level/oss/demo/.utbench/project_profile.json",
	} {
		if _, err := os.Stat(filepath.Join(datasetRoot, "l1", rel)); err != nil {
			t.Fatalf("expected repo-level workspace file to be copied: %s (%v)", rel, err)
		}
	}
}
