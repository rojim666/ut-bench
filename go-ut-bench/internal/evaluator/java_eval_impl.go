package evaluator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go-ut-bench/internal/contracts"
)

func init() {
	RegisterLanguageEvaluator("java", &JavaEvaluator{})
}

// JavaEvaluator Java 语言评测器
type JavaEvaluator struct{}

func (e *JavaEvaluator) PrepareWorkspace(item contracts.GeneratedCase) (*WorkspaceContext, error) {
	if isRepoLevelSample(item.SamplePath) {
		workdir, testRel, moduleDir, className, testClassName, prepErr := prepareJavaRepoLevelWorkspace(item.GeneratedTestPath, item.SamplePath)
		if workdir == "" {
			return nil, errors.New(prepErr)
		}
		return &WorkspaceContext{
			Workdir:  workdir,
			TestPath: testRel,
			Extra: map[string]string{
				"isRepoLevel":   "true",
				"moduleDir":     moduleDir,
				"className":     className,
				"testClassName": testClassName,
			},
			ShouldCleanup: true,
		}, nil
	}
	workdir, testFileName, _, className := prepareJavaWorkspace(item.GeneratedTestPath, item.SamplePath)
	if workdir == "" {
		// prepareJavaWorkspace 在失败时将错误信息存在 testFileName 中
		return nil, errors.New(testFileName)
	}
	return &WorkspaceContext{
		Workdir:       workdir,
		TestPath:      testFileName,
		Extra:         map[string]string{"className": className},
		ShouldCleanup: true,
	}, nil
}

func (e *JavaEvaluator) CompileCheck(ws *WorkspaceContext) (bool, string) {
	if ws.Extra["isRepoLevel"] == "true" {
		return javaCompileCheckRepoLevel(ws.Workdir, ws.Extra["moduleDir"])
	}
	return javaCompileCheck(ws.Workdir)
}

func (e *JavaEvaluator) ExecuteTests(ws *WorkspaceContext, timeoutSeconds int) (bool, string, int) {
	if ws.Extra["isRepoLevel"] == "true" {
		return executeJavaTestsRepoLevel(ws.Workdir, ws.Extra["moduleDir"], ws.Extra["testClassName"], timeoutSeconds)
	}
	return executeJavaTestsWithTimeout(ws.Workdir, timeoutSeconds)
}

func (e *JavaEvaluator) ParseTestCounts(testOutput string) (*int, *int) {
	return parseJavaTestCounts(testOutput)
}

func (e *JavaEvaluator) EstimateAssertionDensity(workdir, testPath string) (int, int, float64) {
	if strings.Contains(filepath.ToSlash(testPath), "/src/test/") {
		raw, err := os.ReadFile(filepath.Join(workdir, filepath.FromSlash(testPath)))
		if err != nil {
			return 0, 0, 0
		}
		return estimateJavaAssertionDensity(string(raw))
	}
	fullPath := filepath.Join(workdir, "src", "test", "java", testPath)
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return 0, 0, 0
	}
	return estimateJavaAssertionDensity(string(raw))
}

func (e *JavaEvaluator) CollectCoverage(ws *WorkspaceContext, testPassed bool, timeoutSeconds int) (float64, float64, string) {
	className := ws.Extra["className"]
	if className == "" {
		return 0, 0, ""
	}
	if ws.Extra["isRepoLevel"] == "true" {
		return collectJavaCoverageRepoLevel(ws.Workdir, ws.Extra["moduleDir"], className, ws.Extra["testClassName"], timeoutSeconds)
	}
	return collectJavaCoverage(ws.Workdir, className)
}

func (e *JavaEvaluator) CollectMutation(ctx context.Context, ws *WorkspaceContext, input MutationInput) (float64, mutationStats, string) {
	className := ws.Extra["className"]
	if ws.Extra["isRepoLevel"] == "true" {
		return 0, mutationStats{}, "pitest: java repo_level mutation is not implemented yet"
	}
	return collectJavaMutation(ctx, ws.Workdir, className, input.TimeoutSeconds, input.TestPassRate, input.TestPassed, input.TestTotal)
}

func (e *JavaEvaluator) MutationTool() string {
	return "pitest"
}
