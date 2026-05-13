package runner

import (
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
