package runner

import "go-ut-bench/internal/contracts"

type generationStrategySpec struct {
	DatasetMode              contracts.DatasetMode
	GenerationStrategy       contracts.GenerationStrategy
	EvaluationStrategy       contracts.EvaluationStrategy
	PromptMode               PromptMode
	RequireGeneratedTestFile bool
}

func resolveGenerationStrategy(sample contracts.SampleRef) generationStrategySpec {
	mode := contracts.DatasetModeForClass(sample.Category)
	if loadRepoLevelMetaForRunner(sample.Path) != nil {
		mode = contracts.DatasetModeProjectLevel
	}

	spec := generationStrategySpec{
		DatasetMode:        mode,
		GenerationStrategy: contracts.GenerationStrategyForMode(mode),
		EvaluationStrategy: contracts.EvaluationStrategyForMode(mode),
		PromptMode:         PromptModeFullFile,
	}
	if mode == contracts.DatasetModeProjectLevel {
		spec.PromptMode = PromptModeRepoLevel
	} else {
		spec.RequireGeneratedTestFile = true
	}
	return spec
}
