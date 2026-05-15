package evaluator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestJavaRepoLevelPrepareWorkspaceUsesProjectRootAndModuleDir(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "java", "java_code_files_repo_level", "oss", "gson")
	moduleRoot := filepath.Join(repo, "gson")
	sourceDir := filepath.Join(moduleRoot, "src", "main", "java", "com", "google", "gson")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "JsonParser.java")
	if err := os.WriteFile(sourcePath, []byte("package com.google.gson;\n\nclass JsonParser {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generatedTest := filepath.Join(root, "JsonParserTest.java")
	if err := os.WriteFile(generatedTest, []byte("import org.junit.jupiter.api.Test;\nclass JsonParserTest { @Test void parses() {} }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := (&JavaEvaluator{}).PrepareWorkspace(contracts.GeneratedCase{
		Language:          "java",
		SampleID:          "json_parser",
		SamplePath:        sourcePath,
		GeneratedTestPath: generatedTest,
		Success:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(ws.Workdir)

	if ws.Extra["isRepoLevel"] != "true" {
		t.Fatalf("expected repo_level workspace, got %+v", ws.Extra)
	}
	if ws.Extra["moduleDir"] != "gson" {
		t.Fatalf("moduleDir = %q", ws.Extra["moduleDir"])
	}
	wantTest := "gson/src/test/java/com/google/gson/JsonParserTest.java"
	if ws.TestPath != wantTest {
		t.Fatalf("test path = %q, want %q", ws.TestPath, wantTest)
	}
	raw, err := os.ReadFile(filepath.Join(ws.Workdir, filepath.FromSlash(wantTest)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "package com.google.gson;") {
		t.Fatalf("generated test package was not normalized:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(ws.Workdir, "pom.xml")); err != nil {
		t.Fatalf("root pom not copied: %v", err)
	}
}
