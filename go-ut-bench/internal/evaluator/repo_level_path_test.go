package evaluator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoLevelSampleDetectionAcceptsBackslashPaths(t *testing.T) {
	root := t.TempDir()
	sampleDir := filepath.Join(root, "datasets", "go", "go_code_files_repo_level", "dogfood", "go-ut-bench", "internal", "runner")
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		t.Fatalf("mkdir sample dir: %v", err)
	}

	samplePath := filepath.Join(sampleDir, "adapter.go")
	if err := os.WriteFile(samplePath, []byte("package runner\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	meta := `{"module_import":"go-ut-bench/internal/runner","package_name":"runner","target_file":"internal/runner/adapter.go","workspace_root":"../.."}`
	if err := os.WriteFile(filepath.Join(sampleDir, "adapter.meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	backslashPath := strings.ReplaceAll(filepath.ToSlash(samplePath), "/", `\`)
	if !isRepoLevelSample(backslashPath) {
		t.Fatalf("expected backslash path to be detected as repo_level: %s", backslashPath)
	}

	loaded := loadRepoLevelMeta(backslashPath)
	if loaded == nil {
		t.Fatalf("expected metadata to load for backslash path")
	}
	wantWorkspace := filepath.Clean(filepath.Join(sampleDir, "../.."))
	if filepath.Clean(loaded.WorkspaceRoot) != wantWorkspace {
		t.Fatalf("workspace root mismatch: got %q want %q", loaded.WorkspaceRoot, wantWorkspace)
	}
}
