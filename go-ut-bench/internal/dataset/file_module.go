package dataset

import (
	"path/filepath"
	"strings"

	"go-ut-bench/internal/contracts"
)

// ResolveFileModule 把样本解析为生成/评测共用的文件模块。
// single_file 只记录源文件本身；repo_level 会解析 workspace、target_file、
// package_dir 和建议的生成测试文件名。
func ResolveFileModule(sample contracts.SampleRef, generatedTestPath string) contracts.FileModule {
	mode := contracts.DatasetModeForClass(sample.Category)
	module := contracts.FileModule{
		DatasetMode:       string(mode),
		TargetFileAbs:     filepath.Clean(sample.Path),
		GeneratedTestPath: generatedTestPath,
	}
	if mode != contracts.DatasetModeProjectLevel {
		module.TargetFile = filepath.Base(sample.Path)
		module.PackageDir = "."
		module.GeneratedTestFile = filepath.Base(generatedTestPath)
		return module
	}

	meta, ok := LoadRepoLevelMetaForSample(sample.Path)
	if !ok {
		return module
	}
	workspaceRoot := strings.TrimSpace(meta.WorkspaceRoot)
	if workspaceRoot != "" && !filepath.IsAbs(workspaceRoot) {
		workspaceRoot = filepath.Join(filepath.Dir(sample.Path), filepath.FromSlash(workspaceRoot))
	}
	targetFile := filepath.ToSlash(strings.TrimSpace(meta.TargetFile))
	packageDir := filepath.ToSlash(filepath.Dir(targetFile))
	if packageDir == "" || packageDir == "." {
		packageDir = "."
	}
	module.WorkspaceRoot = filepath.Clean(workspaceRoot)
	module.TargetFile = targetFile
	if workspaceRoot != "" && targetFile != "" {
		module.TargetFileAbs = filepath.Clean(filepath.Join(workspaceRoot, filepath.FromSlash(targetFile)))
	}
	module.PackageDir = packageDir
	module.PackageName = meta.PackageName
	module.ModuleImport = meta.ModuleImport
	module.GeneratedTestFile = generatedTestFileNameForTarget(targetFile, sample.Language, generatedTestPath)
	return module
}

// LoadRepoLevelMetaForSample 统一加载 repo_level 元数据；没有 sidecar 时尝试自动推断。
func LoadRepoLevelMetaForSample(samplePath string) (*contracts.RepoLevelMeta, bool) {
	if meta, ok := loadRepoLevelMeta(samplePath); ok {
		return meta, true
	}
	return SynthesizeRepoLevelMeta(samplePath)
}

func generatedTestFileNameForTarget(targetFile, language, generatedTestPath string) string {
	base := filepath.Base(targetFile)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "go":
		if stem != "" {
			return stem + "_generated_test.go"
		}
	case "python":
		if stem != "" {
			return "test_" + stem + "_generated.py"
		}
	case "java":
		if stem != "" {
			return stem + "GeneratedTest.java"
		}
	case "cpp", "c++":
		if stem != "" {
			return stem + "_generated_test.cpp"
		}
	}
	return filepath.Base(generatedTestPath)
}
