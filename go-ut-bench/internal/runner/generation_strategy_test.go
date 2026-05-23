package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestResolveGenerationStrategySingleFile(t *testing.T) {
	spec := resolveGenerationStrategy(contracts.SampleRef{Category: contracts.DatasetClassSelfContained})
	if spec.DatasetMode != contracts.DatasetModeSingleFile {
		t.Fatalf("dataset mode = %s, want %s", spec.DatasetMode, contracts.DatasetModeSingleFile)
	}
	if spec.PromptMode != PromptModeFullFile {
		t.Fatalf("prompt mode = %s, want %s", spec.PromptMode, PromptModeFullFile)
	}
	if !spec.RequireGeneratedTestFile {
		t.Fatal("single_file strategy should require one generated test file")
	}
}

func TestResolveGenerationStrategySelfContainedIgnoresSyntheticRepoMeta(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/demo\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sampleDir := filepath.Join(tmp, "go_code_files_self_contained", "boundary")
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	samplePath := filepath.Join(sampleDir, "boundary_000.go")
	if err := os.WriteFile(samplePath, []byte("package main\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := resolveGenerationStrategy(contracts.SampleRef{
		Category: contracts.DatasetClassSelfContained,
		Language: "go",
		Path:     samplePath,
	})
	if spec.DatasetMode != contracts.DatasetModeSingleFile {
		t.Fatalf("dataset mode = %s, want %s", spec.DatasetMode, contracts.DatasetModeSingleFile)
	}
	if spec.PromptMode != PromptModeFullFile {
		t.Fatalf("prompt mode = %s, want %s", spec.PromptMode, PromptModeFullFile)
	}

	prompt := buildPrompt("go", samplePath, "package main\n\nfunc Add(a, b int) int { return a + b }\n")
	if !strings.Contains(prompt, "Mode: full_file") {
		t.Fatalf("prompt should stay full_file for self_contained sample:\n%s", prompt)
	}
	if strings.Contains(prompt, "Mode: repo_level") {
		t.Fatalf("prompt unexpectedly used repo_level:\n%s", prompt)
	}
}

func TestResolveGenerationStrategyProjectLevel(t *testing.T) {
	spec := resolveGenerationStrategy(contracts.SampleRef{Category: contracts.DatasetClassRepoLevel})
	if spec.DatasetMode != contracts.DatasetModeProjectLevel {
		t.Fatalf("dataset mode = %s, want %s", spec.DatasetMode, contracts.DatasetModeProjectLevel)
	}
	if spec.PromptMode != PromptModeRepoLevel {
		t.Fatalf("prompt mode = %s, want %s", spec.PromptMode, PromptModeRepoLevel)
	}
	if spec.RequireGeneratedTestFile {
		t.Fatal("project_level strategy should allow project-native test writes")
	}
}
