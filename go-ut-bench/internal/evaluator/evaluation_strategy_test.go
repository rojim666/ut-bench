package evaluator

import (
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestResolveEvaluationStrategyFromPromptMode(t *testing.T) {
	spec := resolveEvaluationStrategy(contracts.GeneratedCase{PromptMode: string(contracts.PromptModeRepoLevel)})
	if spec.DatasetMode != contracts.DatasetModeProjectLevel {
		t.Fatalf("dataset mode = %s, want %s", spec.DatasetMode, contracts.DatasetModeProjectLevel)
	}
	if spec.EvaluationStrategy != contracts.EvaluationStrategyProjectLevel {
		t.Fatalf("evaluation strategy = %s, want %s", spec.EvaluationStrategy, contracts.EvaluationStrategyProjectLevel)
	}
}

func TestResolveEvaluationStrategySingleFileDefault(t *testing.T) {
	spec := resolveEvaluationStrategy(contracts.GeneratedCase{})
	if spec.DatasetMode != contracts.DatasetModeSingleFile {
		t.Fatalf("dataset mode = %s, want %s", spec.DatasetMode, contracts.DatasetModeSingleFile)
	}
}
