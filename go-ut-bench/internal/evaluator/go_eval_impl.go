package evaluator

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"go-ut-bench/internal/contracts"
)

func init() {
	RegisterLanguageEvaluator("go", &GoEvaluator{})
}

// GoEvaluator Go 语言评测器
// 同时支持 self_contained（单文件 + dummy go.mod）与 repo_level（拷贝整个 workspace）。
// repo_level 分支通过 Extra["isRepoLevel"] = "true" 区分，包目录与目标文件存放在
// Extra["packageDir"]、Extra["targetFile"]。
type GoEvaluator struct{}

func (e *GoEvaluator) PrepareWorkspace(item contracts.GeneratedCase) (*WorkspaceContext, error) {
	item.SamplePath = normalizeEvalPathForHost(item.SamplePath)
	item.GeneratedTestPath = normalizeEvalPathForHost(item.GeneratedTestPath)

	if isRepoLevelSample(item.SamplePath) {
		workdir, testFile, packageDir, targetFile, prepErr := prepareGoRepoLevelWorkspace(item.GeneratedTestPath, item.SamplePath)
		if workdir == "" {
			return nil, errors.New(prepErr)
		}
		return &WorkspaceContext{
			Workdir:       workdir,
			TestPath:      testFile,
			SourceBase:    filepath.Base(targetFile),
			ShouldCleanup: true,
			Extra: map[string]string{
				"isRepoLevel": "true",
				"packageDir":  packageDir,
				"targetFile":  targetFile,
			},
		}, nil
	}

	workdir, testFile, sourceBase, prepErr := prepareGoWorkspace(item.GeneratedTestPath, item.SamplePath)
	if workdir == "" {
		return nil, errors.New(prepErr)
	}
	return &WorkspaceContext{
		Workdir:       workdir,
		TestPath:      testFile,
		SourceBase:    sourceBase,
		ShouldCleanup: true,
	}, nil
}

func (e *GoEvaluator) CompileCheck(ws *WorkspaceContext) (bool, string) {
	if ws.Extra["isRepoLevel"] == "true" {
		return goCompileCheckRepoLevel(ws.Workdir, ws.Extra["packageDir"])
	}
	return goCompileCheck(ws.Workdir, ws.TestPath)
}

func (e *GoEvaluator) ExecuteTests(ws *WorkspaceContext, timeoutSeconds int) (bool, string, int) {
	if ws.Extra["isRepoLevel"] == "true" {
		return executeGoTestsRepoLevel(ws.Workdir, ws.Extra["packageDir"])
	}
	return executeGoTests(ws.Workdir, ws.TestPath, ws.SourceBase)
}

func (e *GoEvaluator) ParseTestCounts(testOutput string) (*int, *int) {
	return parseGoTestCounts(testOutput)
}

func (e *GoEvaluator) EstimateAssertionDensity(workdir, testPath string) (int, int, float64) {
	raw, err := os.ReadFile(filepath.Join(workdir, testPath))
	if err != nil {
		return 0, 0, 0
	}
	return estimateGoAssertionDensity(string(raw))
}

func (e *GoEvaluator) CollectCoverage(ws *WorkspaceContext, testPassed bool, timeoutSeconds int) (float64, float64, string) {
	if ws.Extra["isRepoLevel"] == "true" {
		return collectGoCoverageRepoLevel(ws.Workdir, ws.Extra["packageDir"], ws.Extra["targetFile"])
	}
	if ws.SourceBase == "" {
		return 0, 0, ""
	}
	return collectGoCoverage(ws.Workdir, ws.TestPath, ws.SourceBase)
}

func (e *GoEvaluator) CollectMutation(ctx context.Context, ws *WorkspaceContext, input MutationInput) (float64, mutationStats, string) {
	if ws.Extra["isRepoLevel"] == "true" {
		return collectGoMutationRepoLevel(ctx, ws.Workdir, ws.Extra["packageDir"], ws.Extra["targetFile"], input.TimeoutSeconds, input.TestPassRate, input.TestPassed, input.TestTotal)
	}
	return collectGoMutation(ctx, ws.Workdir, ws.TestPath, ws.SourceBase, input.TimeoutSeconds, input.TestPassRate, input.TestPassed, input.TestTotal)
}

func (e *GoEvaluator) MutationTool() string {
	return "go-mutesting"
}
