package dataset

import (
	"path/filepath"
	"sort"
	"strings"

	"go-ut-bench/internal/contracts"
)

// BenchmarkProfile 定义固定评测规格的目录分层与过滤规则。
type BenchmarkProfile struct {
	Name           string
	Levels         []string
	Languages      []string
	DatasetClasses []string
	MaxSamples     int
	Fixed          bool
}

var benchmarkProfiles = map[string]BenchmarkProfile{
	"small": {
		Name:           "small",
		Levels:         []string{"l1"},
		Languages:      []string{"python", "go"},
		DatasetClasses: []string{"self_contained"},
		MaxSamples:     5,
		Fixed:          true,
	},
	"medium": {
		Name:           "medium",
		Levels:         []string{"l1", "l2"},
		Languages:      []string{"python", "go", "java", "cpp"},
		DatasetClasses: []string{"self_contained"},
		MaxSamples:     30,
		Fixed:          true,
	},
	"large": {
		Name:           "large",
		Levels:         []string{"l1", "l2", "l3"},
		Languages:      []string{"python", "go", "java", "cpp"},
		DatasetClasses: []string{"self_contained"},
		MaxSamples:     50,
		Fixed:          true,
	},
	"custom": {
		Name:  "custom",
		Fixed: false,
	},
}

// NormalizeBenchmarkProfile 规范化 benchmark profile 名称。
func NormalizeBenchmarkProfile(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "small", "medium", "large", "custom":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

// ResolveBenchmarkProfile 返回固定规格定义。
func ResolveBenchmarkProfile(value string) (BenchmarkProfile, bool) {
	name := NormalizeBenchmarkProfile(value)
	if name == "" {
		return BenchmarkProfile{}, false
	}
	profile, ok := benchmarkProfiles[name]
	return profile, ok
}

// ApplyBenchmarkProfile 在未指定 manifest 时，将固定规格映射到 level/lang/class/max。
func ApplyBenchmarkProfile(spec contracts.RunSpec) contracts.RunSpec {
	profile, ok := ResolveBenchmarkProfile(spec.BenchmarkProfile)
	if !ok {
		spec.BenchmarkProfile = ""
		return spec
	}
	spec.BenchmarkProfile = profile.Name
	if !profile.Fixed {
		return spec
	}
	spec.DatasetManifest = ""
	spec.DatasetScenario = ""
	spec.DatasetProject = ""
	spec.DatasetLevel = strings.Join(profile.Levels, ",")
	spec.Languages = append([]string{}, profile.Languages...)
	spec.DatasetClasses = append([]string{}, profile.DatasetClasses...)
	if spec.MaxSamples <= 0 || spec.MaxSamples > profile.MaxSamples {
		spec.MaxSamples = profile.MaxSamples
	}
	return spec
}

func parseCSVNormalized(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		token := strings.ToLower(strings.TrimSpace(part))
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func resolveDatasetRoots(spec contracts.RunSpec) []string {
	datasetRoot := absoluteCleanPath(spec.DatasetRoot)
	levels := parseCSVNormalized(spec.DatasetLevel)
	if len(levels) == 0 {
		return []string{datasetRoot}
	}

	baseRoot := datasetRoot
	baseName := strings.ToLower(filepath.Base(datasetRoot))
	for _, level := range levels {
		if baseName == level {
			baseRoot = filepath.Dir(datasetRoot)
			break
		}
	}

	roots := make([]string, 0, len(levels))
	seen := map[string]struct{}{}
	for _, level := range levels {
		root := absoluteCleanPath(filepath.Join(baseRoot, level))
		if len(levels) == 1 && strings.EqualFold(baseName, level) {
			root = datasetRoot
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots
}

func sampleDedupKey(sample contracts.SampleRef) string {
	return strings.Join([]string{
		sample.Language,
		string(sample.Category),
		sample.Scenario,
		sample.ID,
	}, "|")
}
