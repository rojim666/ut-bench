package evaluator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoMutationMatchPatternForFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.go")
	source := `package demo

func NewAdapter() {}

func (a *Adapter) Generate() {}

func helper_1() {}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	pattern := goMutationMatchPatternForFile(path)
	for _, name := range []string{"NewAdapter", "Generate", "helper_1"} {
		if !strings.Contains(pattern, name) {
			t.Fatalf("pattern %q missing %s", pattern, name)
		}
	}
	if !strings.HasPrefix(pattern, "^(") || !strings.HasSuffix(pattern, ")$") {
		t.Fatalf("expected anchored regex, got %q", pattern)
	}
}

func TestNormalizeGoGeneratedTestPackage(t *testing.T) {
	source := []byte("package main\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	got, changed, errMsg := normalizeGoGeneratedTestPackage(source, "runner")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !changed {
		t.Fatal("expected package declaration to be rewritten")
	}
	if !strings.HasPrefix(string(got), "package runner\n") {
		t.Fatalf("unexpected rewritten source:\n%s", got)
	}
}

func TestNormalizeGoGeneratedTestPackageKeepsMatchingPackage(t *testing.T) {
	source := []byte("package runner\n\nfunc TestX(t *testing.T) {}\n")
	got, changed, errMsg := normalizeGoGeneratedTestPackage(source, "runner")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if changed {
		t.Fatal("did not expect rewrite for matching package")
	}
	if string(got) != string(source) {
		t.Fatalf("source changed unexpectedly:\n%s", got)
	}
}

func TestNormalizeGoGeneratedTestPackageRequiresPackageDeclaration(t *testing.T) {
	_, _, errMsg := normalizeGoGeneratedTestPackage([]byte("func TestX(t *testing.T) {}\n"), "runner")
	if errMsg == "" {
		t.Fatal("expected missing package declaration error")
	}
}
