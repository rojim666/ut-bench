package contracts

// DatasetMode 描述样本的结构层次。
// single_file 表示单文件样本，project_level 表示需要完整项目上下文的样本。
type DatasetMode string

const (
	DatasetModeSingleFile   DatasetMode = "single_file"
	DatasetModeProjectLevel DatasetMode = "project_level"
)

// GenerationStrategy 描述生成阶段的输出契约。
type GenerationStrategy string

const (
	GenerationStrategySingleFile   GenerationStrategy = "single_file"
	GenerationStrategyProjectLevel GenerationStrategy = "project_level"
)

// EvaluationStrategy 描述评测阶段的执行策略。
type EvaluationStrategy string

const (
	EvaluationStrategySingleFile   EvaluationStrategy = "single_file"
	EvaluationStrategyProjectLevel EvaluationStrategy = "project_level"
)

// DatasetModeForClass 将历史 dataset class 映射到新的 dataset mode。
func DatasetModeForClass(class DatasetClass) DatasetMode {
	switch class {
	case DatasetClassRepoLevel:
		return DatasetModeProjectLevel
	default:
		return DatasetModeSingleFile
	}
}

// GenerationStrategyForMode 返回 dataset mode 对应的生成策略。
func GenerationStrategyForMode(mode DatasetMode) GenerationStrategy {
	if mode == DatasetModeProjectLevel {
		return GenerationStrategyProjectLevel
	}
	return GenerationStrategySingleFile
}

// EvaluationStrategyForMode 返回 dataset mode 对应的评测策略。
func EvaluationStrategyForMode(mode DatasetMode) EvaluationStrategy {
	if mode == DatasetModeProjectLevel {
		return EvaluationStrategyProjectLevel
	}
	return EvaluationStrategySingleFile
}
