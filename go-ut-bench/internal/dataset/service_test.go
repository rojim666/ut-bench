package dataset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestDiscoverSamplesFromManifest(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "dataset")
	if err := os.MkdirAll(filepath.Join(datasetRoot, "python"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(datasetRoot, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(datasetRoot, "python", "boundary_000.py"), []byte("def x():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(datasetRoot, "go", "complex_dependency_go_complex_dependency_0.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(root, "dataset_l1.json")
	manifest := `{
  "level": "l1",
  "samples": [
    {"id":"boundary_000","language":"python","category":"self_contained","path":"python/boundary_000.py"},
    {"id":"complex_dependency_go_complex_dependency_0","language":"go","category":"repo_level","path":"go/complex_dependency_go_complex_dependency_0.go"}
  ]
}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	spec := contracts.RunSpec{
		RunID:           "r1",
		DatasetRoot:     datasetRoot,
		OutputRoot:      root,
		ConfigPath:      "dummy",
		DatasetManifest: manifestPath,
		DatasetClasses:  []string{"repo_level"},
		Languages:       []string{"go", "python"},
	}

	samples, err := svc.DiscoverSamples(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	if samples[0].Language != "go" {
		t.Fatalf("expected go sample, got %s", samples[0].Language)
	}
	if samples[0].Category != contracts.DatasetClassRepoLevel {
		t.Fatalf("unexpected category: %s", samples[0].Category)
	}
	if !filepath.IsAbs(samples[0].Path) {
		t.Fatalf("expected absolute sample path, got %s", samples[0].Path)
	}
}

func TestDiscoverSamplesScenarioAndRepoLevelClass(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "datasets")
	path := filepath.Join(datasetRoot, "python", "python_code_files_repo_level", "boundary")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "boundary_000.py"), []byte("def f():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// repo_level 样本要求显式 sidecar meta；discovery 仅检查存在性
	if err := os.WriteFile(filepath.Join(path, "boundary_000.meta.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	spec := contracts.RunSpec{
		RunID:           "r2",
		DatasetRoot:     datasetRoot,
		OutputRoot:      root,
		ConfigPath:      "dummy",
		Languages:       []string{"python"},
		DatasetClasses:  []string{"repo_level"},
		DatasetScenario: "boundary",
		MaxSamples:      10,
	}

	samples, err := svc.DiscoverSamples(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	if samples[0].Category != contracts.DatasetClassRepoLevel {
		t.Fatalf("expected class repo_level, got %s", samples[0].Category)
	}
	if samples[0].Scenario != "boundary" {
		t.Fatalf("expected scenario boundary, got %s", samples[0].Scenario)
	}

	spec.DatasetClasses = []string{"self_contained"}
	samples2, err := svc.DiscoverSamples(spec)
	if err == nil {
		t.Fatalf("expected no samples for self_contained filter, got %d", len(samples2))
	}
}

func TestDiscoverSamplesAutoSynthesizesGoRepoLevelSamples(t *testing.T) {
	root := t.TempDir()
	repoRoot := filepath.Join(root, "datasets", "go", "go_code_files_repo_level", "dogfood", "sample_repo")
	pkgDir := filepath.Join(repoRoot, "internal", "calc")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module example.com/sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "calc.go"), []byte("package calc\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "calc_test.go"), []byte("package calc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	samples, err := svc.DiscoverSamples(contracts.RunSpec{
		RunID:           "r_auto_repo",
		DatasetRoot:     filepath.Join(root, "datasets"),
		OutputRoot:      root,
		ConfigPath:      "dummy",
		Languages:       []string{"go"},
		DatasetClasses:  []string{"repo_level"},
		DatasetScenario: "dogfood",
		MaxSamples:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Fatalf("expected 1 auto repo sample, got %d: %+v", len(samples), samples)
	}
	if samples[0].ID != "internal_calc_calc" {
		t.Fatalf("unexpected sample id: %s", samples[0].ID)
	}
	if _, ok := SynthesizeRepoLevelMeta(samples[0].Path); !ok {
		t.Fatalf("expected synthetic repo meta for %s", samples[0].Path)
	}
	if _, err := os.Stat(filepath.Join(pkgDir, "calc.meta.json")); err == nil {
		t.Fatal("auto discovery should not write sidecar meta files")
	}
}

func TestDiscoverSamplesFiltersRepoLevelProject(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "datasets")
	for _, project := range []string{"alpha", "beta"} {
		pkgDir := filepath.Join(datasetRoot, "go", "go_code_files_repo_level", "oss", project, "pkg")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(datasetRoot, "go", "go_code_files_repo_level", "oss", project, "go.mod"), []byte("module example.com/"+project+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, project+".go"), []byte("package pkg\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService()
	samples, err := svc.DiscoverSamples(contracts.RunSpec{
		RunID:           "r_project_filter",
		DatasetRoot:     datasetRoot,
		OutputRoot:      root,
		ConfigPath:      "dummy",
		Languages:       []string{"go"},
		DatasetClasses:  []string{"repo_level"},
		DatasetScenario: "oss",
		DatasetProject:  "beta",
		MaxSamples:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d: %+v", len(samples), samples)
	}
	if !strings.Contains(filepath.ToSlash(samples[0].Path), "/oss/beta/") {
		t.Fatalf("expected beta sample, got %s", samples[0].Path)
	}
	if !filepath.IsAbs(samples[0].Path) {
		t.Fatalf("expected absolute sample path, got %s", samples[0].Path)
	}
}

func TestDiscoverSamplesRepoLevelSkipsTestsAndAcceptsCppCC(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "datasets")

	javaMain := filepath.Join(datasetRoot, "java", "java_code_files_repo_level", "oss", "demo-java", "src", "main", "java", "demo")
	javaTest := filepath.Join(datasetRoot, "java", "java_code_files_repo_level", "oss", "demo-java", "src", "test", "java", "demo")
	if err := os.MkdirAll(javaMain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(javaTest, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(datasetRoot, "java", "java_code_files_repo_level", "oss", "demo-java", "pom.xml"), "<project/>")
	write(filepath.Join(javaMain, "Demo.java"), "package demo;\npublic class Demo {}\n")
	write(filepath.Join(javaMain, "package-info.java"), "@Deprecated\npackage demo;\n")
	write(filepath.Join(javaMain, "module-info.java"), "module demo {}\n")
	write(filepath.Join(javaTest, "DemoTest.java"), "package demo;\npublic class DemoTest {}\n")

	cppRoot := filepath.Join(datasetRoot, "cpp", "cpp_code_files_repo_level", "oss", "demo-cpp")
	if err := os.MkdirAll(filepath.Join(cppRoot, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cppRoot, "test"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(cppRoot, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\n")
	write(filepath.Join(cppRoot, "src", "demo.cc"), "int demo() { return 1; }\n")
	write(filepath.Join(cppRoot, "test", "demo_test.cc"), "int main() { return 0; }\n")

	svc := NewService()
	javaSamples, err := svc.DiscoverSamples(contracts.RunSpec{
		RunID:           "r_java_tests",
		DatasetRoot:     datasetRoot,
		Languages:       []string{"java"},
		DatasetClasses:  []string{"repo_level"},
		DatasetScenario: "oss",
		DatasetProject:  "demo-java",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(javaSamples) != 1 || !strings.HasSuffix(filepath.ToSlash(javaSamples[0].Path), "/src/main/java/demo/Demo.java") {
		t.Fatalf("expected only main Java source, got %+v", javaSamples)
	}

	cppSamples, err := svc.DiscoverSamples(contracts.RunSpec{
		RunID:           "r_cpp_cc",
		DatasetRoot:     datasetRoot,
		Languages:       []string{"cpp"},
		DatasetClasses:  []string{"repo_level"},
		DatasetScenario: "oss",
		DatasetProject:  "demo-cpp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cppSamples) != 1 || !strings.HasSuffix(filepath.ToSlash(cppSamples[0].Path), "/src/demo.cc") {
		t.Fatalf("expected only .cc production source, got %+v", cppSamples)
	}
}

func TestDiscoverSamplesManifestMaxSamplesPerLanguageScenario(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "dataset")
	if err := os.MkdirAll(filepath.Join(datasetRoot, "python"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(datasetRoot, "go"), 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"python/boundary_000.py":           "def f():\n    return 1\n",
		"python/boundary_001.py":           "def f():\n    return 2\n",
		"python/complex_dependency_000.py": "def f():\n    return 3\n",
		"python/complex_dependency_001.py": "def f():\n    return 4\n",
		"go/boundary_000.go":               "package main\n",
		"go/boundary_001.go":               "package main\n",
		"go/complex_dependency_000.go":     "package main\n",
		"go/complex_dependency_001.go":     "package main\n",
	}
	for relPath, content := range files {
		abs := filepath.Join(datasetRoot, relPath)
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifestPath := filepath.Join(root, "dataset_l1.json")
	manifest := `{
  "level": "l1",
  "samples": [
    {"id":"boundary_000","language":"python","category":"self_contained","path":"python/boundary_000.py","scenario":"boundary"},
    {"id":"boundary_001","language":"python","category":"self_contained","path":"python/boundary_001.py","scenario":"boundary"},
    {"id":"complex_dependency_000","language":"python","category":"self_contained","path":"python/complex_dependency_000.py","scenario":"complex_dependency"},
    {"id":"complex_dependency_001","language":"python","category":"self_contained","path":"python/complex_dependency_001.py","scenario":"complex_dependency"},
    {"id":"boundary_000","language":"go","category":"self_contained","path":"go/boundary_000.go","scenario":"boundary"},
    {"id":"boundary_001","language":"go","category":"self_contained","path":"go/boundary_001.go","scenario":"boundary"},
    {"id":"complex_dependency_000","language":"go","category":"self_contained","path":"go/complex_dependency_000.go","scenario":"complex_dependency"},
    {"id":"complex_dependency_001","language":"go","category":"self_contained","path":"go/complex_dependency_001.go","scenario":"complex_dependency"}
  ]
}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	spec := contracts.RunSpec{
		RunID:           "r3",
		DatasetRoot:     datasetRoot,
		OutputRoot:      root,
		ConfigPath:      "dummy",
		DatasetManifest: manifestPath,
		Languages:       []string{"python", "go"},
		MaxSamples:      1,
	}

	samples, err := svc.DiscoverSamples(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 4 {
		t.Fatalf("expected 4 samples (2 langs x 2 scenarios x 1 each), got %d", len(samples))
	}

	type key struct {
		lang     string
		scenario string
	}
	counts := map[key]int{}
	for _, sample := range samples {
		counts[key{lang: sample.Language, scenario: sample.Scenario}]++
	}

	expected := []key{
		{lang: "python", scenario: "boundary"},
		{lang: "python", scenario: "complex_dependency"},
		{lang: "go", scenario: "boundary"},
		{lang: "go", scenario: "complex_dependency"},
	}
	for _, k := range expected {
		if counts[k] != 1 {
			t.Fatalf("expected 1 sample for %s/%s, got %d", k.lang, k.scenario, counts[k])
		}
	}
}

func TestValidateReadinessFindsCountsErrorsAndRiskWarnings(t *testing.T) {
	root := t.TempDir()
	datasetRoot := filepath.Join(root, "datasets")
	pythonDir := filepath.Join(datasetRoot, "python", "python_code_files_self_contained", "simple_function")
	pythonBoundaryDir := filepath.Join(datasetRoot, "python", "python_code_files_self_contained", "boundary")
	goDir := filepath.Join(datasetRoot, "go", "go_code_files_self_contained", "unknown_bucket")
	javaDir := filepath.Join(datasetRoot, "java")
	cppDir := filepath.Join(datasetRoot, "cpp")
	for _, dir := range []string{pythonDir, pythonBoundaryDir, goDir, javaDir, cppDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pySource := "import itertools\nimport smtplib\n\ndef task_func(x):\n    return list(itertools.combinations(x, 2))\n"
	if err := os.WriteFile(filepath.Join(pythonDir, "simple_function_000.py"), []byte(pySource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pythonBoundaryDir, "simple_function_000.py"), []byte("def task_func():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goDir, "simple_function_000.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := NewService().ValidateReadiness(ValidateOptions{
		DatasetRoot: datasetRoot,
		Languages:   contracts.SupportedLanguages,
		Classes:     []string{"self_contained"},
	})
	if report.Total != 3 {
		t.Fatalf("expected 3 samples, got %d", report.Total)
	}
	if len(report.Errors) == 0 {
		t.Fatalf("expected validation errors")
	}
	if !hasValidationCode(report.Errors, "duplicate_sample_id") {
		t.Fatalf("expected duplicate sample id error: %+v", report.Errors)
	}
	if !hasValidationCode(report.Warnings, "external_network_io") || !hasValidationCode(report.Warnings, "exponential_complexity") {
		t.Fatalf("expected risk warnings: %+v", report.Warnings)
	}
}

func hasValidationCode(items []ValidationIssue, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}
