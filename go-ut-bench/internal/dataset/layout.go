package dataset

import (
	"path/filepath"
	"strings"

	"go-ut-bench/internal/contracts"
)

const (
	datasetClassDirSelfContained = "self_contained"
	datasetClassDirRepoLevel     = "repo_level"
)

func normalizeDatasetClassDirName(part string) string {
	part = strings.ToLower(strings.TrimSpace(part))
	switch part {
	case datasetClassDirSelfContained:
		return datasetClassDirSelfContained
	case datasetClassDirRepoLevel:
		return datasetClassDirRepoLevel
	}
	switch {
	case strings.HasSuffix(part, "_code_files_"+datasetClassDirSelfContained):
		return datasetClassDirSelfContained
	case strings.HasSuffix(part, "_code_files_"+datasetClassDirRepoLevel):
		return datasetClassDirRepoLevel
	default:
		return ""
	}
}

// NormalizeDatasetClassDirName 统一识别新旧数据集 class 目录名。
func NormalizeDatasetClassDirName(part string) string {
	return normalizeDatasetClassDirName(part)
}

func datasetClassDirCandidates(lang string, class string) []string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	class = normalizeDatasetClassDirName(class)
	if lang == "" || class == "" {
		return nil
	}
	return []string{class, lang + "_code_files_" + class}
}

// DatasetClassDirCandidates 返回某语言/类别可能使用的新旧目录名。
func DatasetClassDirCandidates(lang string, class string) []string {
	return datasetClassDirCandidates(lang, class)
}

func splitDatasetRelativeLayout(relPath string) (class string, scenario string, remainder []string, ok bool) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(relPath)), "/")
	if len(parts) < 2 {
		return "", "", nil, false
	}
	class = normalizeDatasetClassDirName(parts[0])
	if class == "" {
		return "", "", nil, false
	}
	scenario = normalizeScenario(parts[1])
	if scenario == "" {
		scenario = parts[1]
	}
	return class, scenario, parts[2:], true
}

func stableSelfContainedSampleID(baseID, scenario string) string {
	baseID = strings.TrimSpace(baseID)
	scenario = normalizeScenario(scenario)
	if baseID == "" || scenario == "" || scenario == "unknown" {
		return baseID
	}
	if baseID == scenario || strings.HasPrefix(baseID, scenario+"_") {
		return baseID
	}
	return scenario + "_" + baseID
}

func buildStableSampleID(baseID, relPath string, class contracts.DatasetClass, scenario string) string {
	if class != contracts.DatasetClassSelfContained {
		return baseID
	}
	if scenario == "" || scenario == "unknown" {
		if _, derivedScenario, _, ok := splitDatasetRelativeLayout(relPath); ok {
			scenario = derivedScenario
		}
	}
	return stableSelfContainedSampleID(baseID, scenario)
}
