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
	if repoLevelMetaForSample(sample) != nil {
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

func repoLevelMetaForSample(sample contracts.SampleRef) *repoLevelMetaForRunner {
	switch sample.Category {
	case contracts.DatasetClassSelfContained:
		return nil
	case contracts.DatasetClassRepoLevel:
		return loadRepoLevelMetaForRunner(sample.Path)
	default:
		return loadRepoLevelMetaForRunner(sample.Path)
	}
}
