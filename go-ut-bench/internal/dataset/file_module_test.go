package dataset

import (
	"os"
	"path/filepath"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestResolveFileModuleSynthesizesGoRepoLevelModule(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "go", "go_code_files_repo_level", "oss", "demo")
	pkgDir := filepath.Join(repo, "internal", "calc")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(pkgDir, "adder.go")
	if err := os.WriteFile(sourcePath, []byte("package calc\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	module := ResolveFileModule(contracts.SampleRef{
		ID:       "internal_calc_adder",
		Language: "go",
		Category: contracts.DatasetClassRepoLevel,
		Path:     sourcePath,
	}, filepath.Join(root, "artifacts", "generated", "tests", "model", "go", "internal_calc_adder.test.go"))

	if module.DatasetMode != string(contracts.DatasetModeProjectLevel) {
		t.Fatalf("dataset mode = %q", module.DatasetMode)
	}
	if module.TargetFile != "internal/calc/adder.go" {
		t.Fatalf("target file = %q", module.TargetFile)
	}
	if module.PackageDir != "internal/calc" {
		t.Fatalf("package dir = %q", module.PackageDir)
	}
	if module.PackageName != "calc" {
		t.Fatalf("package name = %q", module.PackageName)
	}
	if module.ModuleImport != "example.com/demo/internal/calc" {
		t.Fatalf("module import = %q", module.ModuleImport)
	}
	if module.GeneratedTestFile != "adder_generated_test.go" {
		t.Fatalf("generated test file = %q", module.GeneratedTestFile)
	}
}

func TestResolveFileModuleSynthesizesPythonRepoLevelModule(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "python", "python_code_files_repo_level", "oss", "humanize")
	pkgDir := filepath.Join(repo, "src", "humanize")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pyproject.toml"), []byte("[project]\nname='humanize'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(pkgDir, "number.py")
	if err := os.WriteFile(sourcePath, []byte("def intcomma(value):\n    return str(value)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	module := ResolveFileModule(contracts.SampleRef{
		ID:       "src_humanize_number",
		Language: "python",
		Category: contracts.DatasetClassRepoLevel,
		Path:     sourcePath,
	}, filepath.Join(root, "artifacts", "generated", "tests", "model", "python", "src_humanize_number.test.py"))

	if module.TargetFile != "src/humanize/number.py" {
		t.Fatalf("target file = %q", module.TargetFile)
	}
	if module.ModuleImport != "humanize.number" {
		t.Fatalf("module import = %q", module.ModuleImport)
	}
	if module.PackageName != "humanize" {
		t.Fatalf("package name = %q", module.PackageName)
	}
	if module.GeneratedTestFile != "test_number_generated.py" {
		t.Fatalf("generated test file = %q", module.GeneratedTestFile)
	}
}

func TestResolveFileModuleSynthesizesJavaRepoLevelModule(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "java", "java_code_files_repo_level", "oss", "gson")
	pkgDir := filepath.Join(repo, "src", "main", "java", "com", "google", "gson")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(pkgDir, "JsonParser.java")
	if err := os.WriteFile(sourcePath, []byte("package com.google.gson;\n\nclass JsonParser {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	module := ResolveFileModule(contracts.SampleRef{
		ID:       "src_main_java_com_google_gson_jsonparser",
		Language: "java",
		Category: contracts.DatasetClassRepoLevel,
		Path:     sourcePath,
	}, filepath.Join(root, "artifacts", "generated", "tests", "model", "java", "jsonparser.test.java"))

	if module.PackageName != "com.google.gson" {
		t.Fatalf("package name = %q", module.PackageName)
	}
	if module.ModuleImport != "com.google.gson.JsonParser" {
		t.Fatalf("module import = %q", module.ModuleImport)
	}
	if module.GeneratedTestFile != "JsonParserGeneratedTest.java" {
		t.Fatalf("generated test file = %q", module.GeneratedTestFile)
	}
}

func TestSynthesizeJavaRepoLevelMetaPrefersDatasetProjectRoot(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "java", "java_code_files_repo_level", "oss", "gson")
	moduleRoot := filepath.Join(repo, "gson")
	pkgDir := filepath.Join(moduleRoot, "src", "main", "java", "com", "google", "gson")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(pkgDir, "JsonParser.java")
	if err := os.WriteFile(sourcePath, []byte("package com.google.gson;\n\nclass JsonParser {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, ok := SynthesizeRepoLevelMeta(sourcePath)
	if !ok {
		t.Fatal("expected synthetic Java repo metadata")
	}
	workspaceAbs := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(meta.WorkspaceRoot)))
	if workspaceAbs != repo {
		t.Fatalf("workspace root = %q, want %q", workspaceAbs, repo)
	}
	if meta.TargetFile != "gson/src/main/java/com/google/gson/JsonParser.java" {
		t.Fatalf("target file = %q", meta.TargetFile)
	}
}
