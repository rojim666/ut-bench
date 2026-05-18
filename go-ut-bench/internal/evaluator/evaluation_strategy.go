package evaluator

import (
	"strings"

	"go-ut-bench/internal/contracts"
)

type evaluationStrategySpec struct {
	DatasetMode        contracts.DatasetMode
	GenerationStrategy contracts.GenerationStrategy
	EvaluationStrategy contracts.EvaluationStrategy
}

func resolveEvaluationStrategy(item contracts.GeneratedCase) evaluationStrategySpec {
	mode := contracts.DatasetMode(strings.TrimSpace(item.DatasetMode))
	if mode == "" {
		switch {
		case strings.EqualFold(item.EvaluationStrategy, string(contracts.EvaluationStrategyProjectLevel)):
			mode = contracts.DatasetModeProjectLevel
		case strings.EqualFold(item.PromptMode, string(contracts.PromptModeRepoLevel)):
			mode = contracts.DatasetModeProjectLevel
		case strings.Contains(strings.ToLower(filepathSlash(item.SamplePath)), "repo_level"):
			mode = contracts.DatasetModeProjectLevel
		default:
			mode = contracts.DatasetModeSingleFile
		}
	}
	return evaluationStrategySpec{
		DatasetMode:        mode,
		GenerationStrategy: contracts.GenerationStrategyForMode(mode),
		EvaluationStrategy: contracts.EvaluationStrategyForMode(mode),
	}
}

func filepathSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
