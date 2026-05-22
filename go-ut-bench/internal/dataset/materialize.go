package dataset

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-ut-bench/internal/contracts"
)

// MaterializeLevelsOptions 控制 level 目录生成与校验。
type MaterializeLevelsOptions struct {
	DatasetRoot string
	ConfigRoot  string
	Levels      []string
	Check       bool
}

// MaterializeLevelSummary 描述单个 level 的生成结果。
type MaterializeLevelSummary struct {
	Level      string
	Samples    int
	Files      int
	RepoRoots  int
	TargetRoot string
}

// MaterializeLevelsSummary 描述全部 level 的执行结果。
type MaterializeLevelsSummary struct {
	Levels []MaterializeLevelSummary
}

type levelDefinition struct {
	Level   string
	Samples []datasetManifestSample
}

type plannedFile struct {
	SourceAbs string
	TargetRel string
}

// MaterializeLevels 根据 level 定义生成 datasets/l1~l3 目录，或校验现有目录是否漂移。
func (s *Service) MaterializeLevels(opts MaterializeLevelsOptions) (MaterializeLevelsSummary, error) {
	datasetRoot := absoluteCleanPath(opts.DatasetRoot)
	configRoot := absoluteCleanPath(opts.ConfigRoot)
	levels := opts.Levels
	if len(levels) == 0 {
		levels = []string{"l1", "l2", "l3"}
	}

	definitions, err := loadLevelDefinitions(configRoot, datasetRoot, levels)
	if err != nil {
		return MaterializeLevelsSummary{}, err
	}

	summary := MaterializeLevelsSummary{Levels: make([]MaterializeLevelSummary, 0, len(definitions))}
	for _, definition := range definitions {
		targetRoot := filepath.Join(datasetRoot, definition.Level)
		plan, repoRoots, err := buildLevelFilePlan(datasetRoot, definition)
		if err != nil {
			return MaterializeLevelsSummary{}, err
		}
		if opts.Check {
			if err := checkMaterializedLevel(targetRoot, plan); err != nil {
				return MaterializeLevelsSummary{}, err
			}
		} else {
			if err := os.RemoveAll(targetRoot); err != nil {
				return MaterializeLevelsSummary{}, err
			}
			if err := os.MkdirAll(targetRoot, 0o755); err != nil {
				return MaterializeLevelsSummary{}, err
			}
			for _, lang := range contracts.SupportedLanguages {
				if err := os.MkdirAll(filepath.Join(targetRoot, lang), 0o755); err != nil {
					return MaterializeLevelsSummary{}, err
				}
			}
			for _, file := range plan {
				targetPath := filepath.Join(targetRoot, file.TargetRel)
				if err := copyFilePreserve(file.SourceAbs, targetPath); err != nil {
					return MaterializeLevelsSummary{}, err
				}
			}
		}
		summary.Levels = append(summary.Levels, MaterializeLevelSummary{
			Level:      definition.Level,
			Samples:    len(definition.Samples),
			Files:      len(plan),
			RepoRoots:  repoRoots,
			TargetRoot: targetRoot,
		})
	}
	return summary, nil
}

func loadLevelDefinitions(configRoot, datasetRoot string, levels []string) ([]levelDefinition, error) {
	definitions := make([]levelDefinition, 0, len(levels))
	hasAllLevelManifests := true
	for _, level := range levels {
		path := filepath.Join(configRoot, "dataset_"+strings.ToLower(strings.TrimSpace(level))+".json")
		if _, err := os.Stat(path); err != nil {
			hasAllLevelManifests = false
			break
		}
	}
	if hasAllLevelManifests {
		for _, level := range levels {
			path := filepath.Join(configRoot, "dataset_"+strings.ToLower(strings.TrimSpace(level))+".json")
			manifest, err := loadManifest(path, datasetRoot)
			if err != nil {
				return nil, err
			}
			definitions = append(definitions, levelDefinition{
				Level:   strings.ToLower(strings.TrimSpace(level)),
				Samples: append([]datasetManifestSample{}, manifest.Samples...),
			})
		}
		return definitions, nil
	}
	return deriveLevelDefinitionsFromProfiles(configRoot, datasetRoot, levels)
}

func deriveLevelDefinitionsFromProfiles(configRoot, datasetRoot string, levels []string) ([]levelDefinition, error) {
	load := func(name string) (datasetManifest, error) {
		return loadManifest(filepath.Join(configRoot, "dataset_"+name+".json"), datasetRoot)
	}
	small, err := load("small")
	if err != nil {
		return nil, err
	}
	medium, err := load("medium")
	if err != nil {
		return nil, err
	}
	large, err := load("large")
	if err != nil {
		return nil, err
	}

	smallByKey := manifestSampleSet(small.Samples)
	mediumByKey := manifestSampleSet(medium.Samples)
	largeByKey := manifestSampleSet(large.Samples)
	for key := range smallByKey {
		if _, ok := mediumByKey[key]; !ok {
			return nil, fmt.Errorf("small benchmark manifest is not a subset of medium: %s", key)
		}
	}
	for key := range mediumByKey {
		if _, ok := largeByKey[key]; !ok {
			return nil, fmt.Errorf("medium benchmark manifest is not a subset of large: %s", key)
		}
	}

	derived := map[string][]datasetManifestSample{
		"l1": append([]datasetManifestSample{}, small.Samples...),
		"l2": diffManifestSamples(medium.Samples, smallByKey),
		"l3": diffManifestSamples(large.Samples, mediumByKey),
	}
	definitions := make([]levelDefinition, 0, len(levels))
	for _, level := range levels {
		level = strings.ToLower(strings.TrimSpace(level))
		samples, ok := derived[level]
		if !ok {
			return nil, fmt.Errorf("unsupported level for derivation: %s", level)
		}
		definitions = append(definitions, levelDefinition{
			Level:   level,
			Samples: samples,
		})
	}
	return definitions, nil
}

func manifestSampleSet(samples []datasetManifestSample) map[string]datasetManifestSample {
	out := make(map[string]datasetManifestSample, len(samples))
	for _, sample := range samples {
		out[manifestSampleKey(sample)] = sample
	}
	return out
}

func diffManifestSamples(samples []datasetManifestSample, base map[string]datasetManifestSample) []datasetManifestSample {
	out := make([]datasetManifestSample, 0, len(samples))
	for _, sample := range samples {
		if _, ok := base[manifestSampleKey(sample)]; ok {
			continue
		}
		out = append(out, sample)
	}
	return out
}

func manifestSampleKey(sample datasetManifestSample) string {
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(sample.Language)),
		string(sample.Category),
		strings.TrimSpace(sample.Scenario),
		strings.TrimSpace(sample.ID),
		filepath.ToSlash(filepath.Clean(sample.Path)),
	}, "|")
}

func buildLevelFilePlan(datasetRoot string, definition levelDefinition) ([]plannedFile, int, error) {
	repoRoots := map[string]struct{}{}
	fileSet := map[string]plannedFile{}
	for _, sample := range definition.Samples {
		rel, err := filepath.Rel(datasetRoot, sample.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, 0, fmt.Errorf("sample path is outside dataset root: %s", sample.Path)
		}
		rel = filepath.Clean(rel)
		if sample.Category == contracts.DatasetClassRepoLevel {
			projectRoot, ok := repoLevelProjectRel(rel)
			if !ok {
				return nil, 0, fmt.Errorf("invalid repo-level sample path: %s", rel)
			}
			repoRoots[projectRoot] = struct{}{}
			projectAbs := filepath.Join(datasetRoot, projectRoot)
			err := filepath.WalkDir(projectAbs, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					return nil
				}
				childRel, err := filepath.Rel(datasetRoot, path)
				if err != nil {
					return err
				}
				targetRel := filepath.Clean(childRel)
				fileSet[targetRel] = plannedFile{SourceAbs: path, TargetRel: targetRel}
				return nil
			})
			if err != nil {
				return nil, 0, err
			}
			continue
		}
		fileSet[rel] = plannedFile{SourceAbs: sample.Path, TargetRel: rel}
	}

	keys := make([]string, 0, len(fileSet))
	for key := range fileSet {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	plan := make([]plannedFile, 0, len(keys))
	for _, key := range keys {
		plan = append(plan, fileSet[key])
	}
	return plan, len(repoRoots), nil
}

func repoLevelProjectRel(rel string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) < 4 {
		return "", false
	}
	if !strings.Contains(parts[1], "_code_files_repo_level") {
		return "", false
	}
	return filepath.FromSlash(strings.Join(parts[:4], "/")), true
}

func copyFilePreserve(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(0o644)
}

func checkMaterializedLevel(targetRoot string, plan []plannedFile) error {
	expected := make(map[string]string, len(plan))
	for _, file := range plan {
		expected[file.TargetRel] = file.SourceAbs
	}

	actual := map[string]string{}
	if err := filepath.WalkDir(targetRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(targetRoot, path)
		if err != nil {
			return err
		}
		actual[filepath.Clean(rel)] = path
		return nil
	}); err != nil {
		return err
	}

	for rel, src := range expected {
		dst, ok := actual[rel]
		if !ok {
			return fmt.Errorf("materialized level is missing file: %s", filepath.Join(targetRoot, rel))
		}
		same, err := filesShareMD5(src, dst)
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("materialized level file drift detected: %s", filepath.Join(targetRoot, rel))
		}
		delete(actual, rel)
	}
	if len(actual) > 0 {
		extras := make([]string, 0, len(actual))
		for rel := range actual {
			extras = append(extras, filepath.Join(targetRoot, rel))
		}
		sort.Strings(extras)
		return fmt.Errorf("materialized level contains unexpected files: %s", strings.Join(extras, ", "))
	}
	return nil
}

func filesShareMD5(src, dst string) (bool, error) {
	srcSum, err := fileMD5(src)
	if err != nil {
		return false, err
	}
	dstSum, err := fileMD5(dst)
	if err != nil {
		return false, err
	}
	return srcSum == dstSum, nil
}
