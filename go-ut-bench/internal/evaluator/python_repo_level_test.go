package evaluator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestPythonRepoLevelPrepareWorkspaceCopiesProjectAndAddsSrcPath(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "python", "python_code_files_repo_level", "oss", "humanize")
	srcDir := filepath.Join(repo, "src", "humanize")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pyproject.toml"), []byte("[project]\nname = \"humanize\"\n\n[tool.hatch]\nbuild.hooks.vcs.version-file = \"src/humanize/_version.py\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(srcDir, "number.py")
	if err := os.WriteFile(sourcePath, []byte("def intword(value):\n    return str(value)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generatedTest := filepath.Join(root, "test_number.py")
	if err := os.WriteFile(generatedTest, []byte("import humanize\n\ndef test_intword():\n    assert humanize.number.intword(3) == \"3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := (&PythonEvaluator{}).PrepareWorkspace(contracts.GeneratedCase{
		Language:          "python",
		SampleID:          "src_humanize_number",
		SamplePath:        sourcePath,
		GeneratedTestPath: generatedTest,
		Success:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(ws.Workdir)

	if ws.Workdir == repo {
		t.Fatalf("repo_level workspace should use temp copy, got original repo %s", ws.Workdir)
	}
	if !ws.ShouldCleanup {
		t.Fatal("repo_level workspace should be cleaned up")
	}
	if ws.TestPath != filepath.Join("tests", "test_number.py") {
		t.Fatalf("test path = %q", ws.TestPath)
	}
	if _, err := os.Stat(filepath.Join(repo, "tests", "test_number.py")); !os.IsNotExist(err) {
		t.Fatalf("original dataset should not be modified, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Workdir, "src", "humanize", "number.py")); err != nil {
		t.Fatalf("target file not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Workdir, "src", "humanize", "_version.py")); err != nil {
		t.Fatalf("generated version file not prepared: %v", err)
	}
	env := pythonWorkspaceEnv(ws.Workdir)
	pyPath := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "PYTHONPATH=") {
			pyPath = strings.TrimPrefix(entry, "PYTHONPATH=")
			break
		}
	}
	if !strings.Contains(pyPath, ws.Workdir) || !strings.Contains(pyPath, filepath.Join(ws.Workdir, "src")) {
		t.Fatalf("PYTHONPATH missing workspace/src entries: %q", pyPath)
	}
}

func TestBuildMutmutPyprojectRestrictsPytestToGeneratedTest(t *testing.T) {
	cfg := buildMutmutPyproject([]string{"src/humanize/number.py"}, "tests/test_number.py", nil)
	if !strings.Contains(cfg, `"tests/test_number.py"`) {
		t.Fatalf("mutmut config should include generated test path:\n%s", cfg)
	}
}
