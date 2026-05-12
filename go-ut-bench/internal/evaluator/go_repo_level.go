// go_repo_level.go 提供 Go 语言 repo_level 样本的评测能力
// 与 self_contained 模式（go_eval.go）的关键差异：
//   - 把 meta.WorkspaceRoot 整个仓库复制到 tmpdir（跳过 *_test.go 与生成产物），
//     保留原仓库的 go.mod / go.sum 与所有内部包，使被测文件能解析其内部依赖
//   - 生成的测试文件落到 target_file 同目录，包名由 LLM 在生成时决定
//   - 编译 / 测试 / 覆盖率均以"包路径"（./<package_dir>）为单位执行
package evaluator

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// prepareGoRepoLevelWorkspace 准备 Go repo_level 评测工作区
//
// 参数:
//   - testPath: 生成的测试文件路径（artifacts/.../xxx.test.go）
//   - samplePath: 数据集中的源文件路径
//
// 返回值（顺序）：
//   - workdir: 评测临时工作目录的绝对路径（即拷贝出来的仓库根）
//   - testFileBase: 写入到 target_file 同目录后的测试文件名（仅文件名）
//   - packageDir: target_file 所在的相对包目录（如 "internal/runner"），用于 `go test ./<packageDir>`
//   - targetFile: meta.TargetFile（相对 workspace 根的源文件路径）
//   - errMsg: 失败原因；为空表示成功
func prepareGoRepoLevelWorkspace(testPath, samplePath string) (workdir, testFileBase, packageDir, targetFile, errMsg string) {
	meta := loadRepoLevelMeta(samplePath)
	if meta == nil {
		return "", "", "", "", "repo_level sample missing metadata"
	}
	if strings.TrimSpace(meta.WorkspaceRoot) == "" {
		return "", "", "", "", "repo_level workspace_root not set in metadata"
	}
	if strings.TrimSpace(meta.TargetFile) == "" {
		return "", "", "", "", "repo_level target_file not set in metadata"
	}
	if _, err := os.Stat(meta.WorkspaceRoot); err != nil {
		return "", "", "", "", "repo_level workspace not found: " + meta.WorkspaceRoot
	}

	testSource, err := os.ReadFile(testPath)
	if err != nil {
		return "", "", "", "", fmt.Sprintf("failed to read generated test: %s", err)
	}

	tmpdir, err := os.MkdirTemp("", "utbench_go_repo_eval_")
	if err != nil {
		return "", "", "", "", fmt.Sprintf("failed to create temp dir: %s", err)
	}

	// 拷贝整个 workspace。跳过：
	//   - *_test.go：避免与生成测试冲突；同时让生成测试是该包内唯一的测试集
	//   - 元数据 sidecar 与覆盖率输出
	skip := func(rel string) bool {
		base := filepath.Base(rel)
		if strings.HasSuffix(base, "_test.go") {
			return true
		}
		if strings.HasSuffix(base, ".meta.json") {
			return true
		}
		if base == "cover.out" || base == "compile_check_output.test" || base == "compile_check_output.test.exe" {
			return true
		}
		return false
	}
	if err := copyTreeFiltered(meta.WorkspaceRoot, tmpdir, skip); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", fmt.Sprintf("failed to copy workspace: %s", err)
	}

	// 校验 target_file 在拷贝后的 workspace 里实际存在
	targetAbs := filepath.Join(tmpdir, filepath.FromSlash(meta.TargetFile))
	if _, err := os.Stat(targetAbs); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", fmt.Sprintf("target_file not found in workspace: %s", meta.TargetFile)
	}

	// 把生成测试写到 target_file 同目录；命名为 <stem>_generated_test.go
	pkgDirAbs := filepath.Dir(targetAbs)
	targetBase := filepath.Base(meta.TargetFile)
	targetStem := strings.TrimSuffix(targetBase, filepath.Ext(targetBase))
	testFileBase = targetStem + "_generated_test.go"
	if strings.TrimSpace(meta.PackageName) != "" {
		var pkgErr string
		testSource, _, pkgErr = normalizeGoGeneratedTestPackage(testSource, meta.PackageName)
		if pkgErr != "" {
			_ = os.RemoveAll(tmpdir)
			return "", "", "", "", pkgErr
		}
	}
	if err := os.WriteFile(filepath.Join(pkgDirAbs, testFileBase), testSource, 0o644); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", fmt.Sprintf("failed to write test: %s", err)
	}

	// packageDir 用相对路径，前缀 "./" 便于直接拼成 `go test ./internal/runner`
	packageDir = filepath.ToSlash(filepath.Dir(meta.TargetFile))
	if packageDir == "" || packageDir == "." {
		packageDir = "."
	}
	return tmpdir, testFileBase, packageDir, meta.TargetFile, ""
}

func normalizeGoGeneratedTestPackage(source []byte, wantPackage string) ([]byte, bool, string) {
	wantPackage = strings.TrimSpace(wantPackage)
	if wantPackage == "" {
		return source, false, ""
	}
	re := regexp.MustCompile(`(?m)^(\s*package\s+)([A-Za-z_][A-Za-z0-9_]*)(\b[^\r\n]*)`)
	match := re.FindSubmatchIndex(source)
	if match == nil {
		return source, false, "generated Go test missing package declaration"
	}
	gotPackage := string(source[match[4]:match[5]])
	if gotPackage == wantPackage {
		return source, false, ""
	}
	fixed := make([]byte, 0, len(source)-len(gotPackage)+len(wantPackage))
	fixed = append(fixed, source[:match[4]]...)
	fixed = append(fixed, wantPackage...)
	fixed = append(fixed, source[match[5]:]...)
	return fixed, true, ""
}

// goCompileCheckRepoLevel 在 repo_level workspace 中编译 <packageDir>
func goCompileCheckRepoLevel(workdir, packageDir string) (bool, string) {
	tempOutput := filepath.Join(workdir, "compile_check_output.test")
	if runtime.GOOS == "windows" {
		tempOutput = filepath.Join(workdir, "compile_check_output.test.exe")
	}
	defer os.Remove(tempOutput)

	runCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeoutSeconds*time.Second)
	defer cancel()
	output, err := runCommandWithProcessGroupKill(runCtx, "go", []string{"test", "-c", "-o", tempOutput, packagePathArg(packageDir)}, workdir, nil)
	if runCtx.Err() != nil {
		return false, fmt.Sprintf("go compile timed out after %ds", defaultTestTimeoutSeconds)
	}
	if err == nil {
		return true, ""
	}
	return false, trimErr(string(output), 2000)
}

// executeGoTestsRepoLevel 在 repo_level workspace 中执行 <packageDir> 的测试
func executeGoTestsRepoLevel(workdir, packageDir string) (bool, string, int) {
	runCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeoutSeconds*time.Second)
	defer cancel()
	started := time.Now()
	args := []string{"test", "-v", fmt.Sprintf("-timeout=%ds", defaultTestTimeoutSeconds), packagePathArg(packageDir)}
	output, err := runCommandWithProcessGroupKill(runCtx, "go", args, workdir, nil)
	latency := int(time.Since(started).Milliseconds())
	if runCtx.Err() != nil {
		return false, fmt.Sprintf("go test timed out after %ds", defaultTestTimeoutSeconds), latency
	}
	if err == nil {
		return true, string(output), latency
	}
	return false, trimErr(string(output), 4000), latency
}

// collectGoCoverageRepoLevel 在 repo_level workspace 中按 <packageDir> 收集覆盖率，
// 并按 targetFile 过滤，只统计目标文件的语句覆盖率
func collectGoCoverageRepoLevel(workdir, packageDir, targetFile string) (float64, float64, string) {
	coverFile := filepath.Join(workdir, "cover.out")
	runCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeoutSeconds*time.Second)
	defer cancel()
	args := []string{
		"test",
		fmt.Sprintf("-timeout=%ds", defaultTestTimeoutSeconds),
		"-coverprofile=cover.out",
		packagePathArg(packageDir),
	}
	out, err := runCommandWithProcessGroupKill(runCtx, "go", args, workdir, nil)
	if runCtx.Err() != nil {
		return 0, 0, fmt.Sprintf("go coverage timed out after %ds", defaultTestTimeoutSeconds)
	}
	if err != nil {
		return 0, 0, "go coverage failed: " + trimErr(string(out), 1000)
	}

	raw, err := os.ReadFile(coverFile)
	if err != nil {
		return 0, 0, fmt.Sprintf("failed to read coverage file: %s", err)
	}

	// 用 target_file 的 basename 复用现有解析函数
	return parseGoCoverageOutput(string(raw), filepath.Base(targetFile))
}

func collectGoMutationRepoLevel(ctx context.Context, workdir, packageDir, targetFile string, timeoutSeconds int, testPassRate *float64, testPassed, testTotal int) (float64, mutationStats, string) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = MutationTimeoutSeconds
	}

	commandWorkdir := filepath.Join(workdir, filepath.FromSlash(packageDir))
	targetArg := "."
	displayTarget := filepath.ToSlash(targetFile)
	targetPath := filepath.Join(workdir, filepath.FromSlash(targetFile))
	fmt.Printf("        [MUTATION] Go go-mutesting 开始 | 目标: %s | 超时: %ds\n", displayTarget, timeoutSeconds)
	logMutation("DEBUG-1", "mutation_start", "language", "go", "tool", "go-mutesting", "source_base", displayTarget, "workdir", commandWorkdir, "target_arg", targetArg, "timeout_seconds", timeoutSeconds)

	minPassRate := GetMinPassRateForTool("go-mutesting")
	passed := 0
	total := 0
	if testPassed > 0 || testTotal > 0 {
		passed = testPassed
		total = testTotal
	} else if testPassRate != nil {
		total = 100
		passed = int(math.Round(*testPassRate * float64(total)))
	}

	checkResult := CheckTestPassRate(passed, total, "go-mutesting", minPassRate)
	if !checkResult.ShouldRun {
		fmt.Printf("        [MUTATION] 跳过: %s\n", checkResult.Message)
		logMutation("DEBUG-2", "mutation_skip", "reason", checkResult.Message)
		return 0, mutationStats{}, checkResult.Message
	}

	if _, err := os.Stat(targetPath); err != nil {
		fmt.Printf("        [MUTATION] 错误: 目标文件不存在\n")
		logMutation("ERROR", "mutation_error", "error", "target file not found", "source_base", displayTarget, "target_path", targetPath)
		return 0, mutationStats{}, fmt.Sprintf("target file not found: %s", displayTarget)
	}

	goMutestingPath := findGoMutesting()
	if goMutestingPath == "" {
		fmt.Printf("        [MUTATION] 错误: go-mutesting 未安装\n")
		logMutation("ERROR", "mutation_error", "error", "go-mutesting not installed")
		return 0, mutationStats{}, "go-mutesting not installed. Install: go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest"
	}

	fmt.Printf("        [MUTATION] 步骤1: 在包目录运行 go-mutesting (超时=%ds)...\n", timeoutSeconds)
	logMutation("DEBUG-1", "mutation_step", "step", "go_mutesting_repo_level", "go_mutesting_path", goMutestingPath, "workdir", commandWorkdir, "target_arg", targetArg)
	mutationRunStart := time.Now()
	runCtx, cancelRun := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancelRun()

	args := []string{targetArg}
	if matchPattern := goMutationMatchPatternForFile(targetPath); matchPattern != "" {
		args = []string{"--match=" + matchPattern, targetArg}
		logMutation("DEBUG-1", "mutation_filter", "match", matchPattern)
	}
	runOut, runErr := runCommandWithProcessGroupKill(runCtx, goMutestingPath, args, commandWorkdir, nil)
	mutationRunElapsed := time.Since(mutationRunStart)
	logMutation("DEBUG-1", "mutation_step_done", "step", "go_mutesting_repo_level", "elapsed_ms", mutationRunElapsed.Milliseconds(), "run_err", runErr)

	if runErr != nil {
		fmt.Printf("        [MUTATION] 步骤1完成(有错误) | 耗时: %dms | 错误: %v\n", mutationRunElapsed.Milliseconds(), runErr)
	} else {
		fmt.Printf("        [MUTATION] 步骤1完成 | 耗时: %dms\n", mutationRunElapsed.Milliseconds())
	}

	stats, parseErr := parseGoMutestingOutput(string(runOut))
	if parseErr != "" {
		return 0, stats, formatMutationToolError("go-mutesting", parseErr, runErr, runOut, nil, nil)
	}
	if stats.Total <= 0 {
		return 0, stats, formatMutationToolError("go-mutesting", "go-mutesting produced zero mutants", runErr, runOut, nil, nil)
	}

	processed := stats.Killed + stats.Survived + stats.NoTests + stats.Timeout + stats.Skipped + stats.Suspicious
	if processed <= 0 {
		return 0, stats, formatMutationToolError("go-mutesting", "go-mutesting did not execute any mutants", runErr, runOut, nil, nil)
	}
	if stats.Killed+stats.Survived <= 0 {
		return 0, stats, formatMutationToolError("go-mutesting", "go-mutesting no killed/survived results", runErr, runOut, nil, nil)
	}

	score := round(float64(stats.Killed)/float64(stats.Total), 6)
	return score, stats, ""
}

// packagePathArg 将 packageDir 转成 `go test` 接受的包路径参数
func goMutationMatchPatternForFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	matches := re.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		return ""
	}
	names := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, match := range matches {
		if len(match) < 2 || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		names = append(names, regexp.QuoteMeta(match[1]))
	}
	if len(names) == 0 {
		return ""
	}
	return "^(" + strings.Join(names, "|") + ")$"
}

func packagePathArg(packageDir string) string {
	if packageDir == "" || packageDir == "." {
		return "."
	}
	if strings.HasPrefix(packageDir, "./") || strings.HasPrefix(packageDir, "../") {
		return packageDir
	}
	return "./" + packageDir
}

// copyTreeFiltered 递归复制 src 到 dst，shouldSkip 返回 true 的相对路径被跳过。
// 保留文件权限，符号链接按目标内容复制为普通文件（够用即可）。
func copyTreeFiltered(src, dst string, shouldSkip func(rel string) bool) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	return filepath.WalkDir(srcAbs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if shouldSkip != nil && shouldSkip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// 跳过非常规文件（设备等），符号链接走 ReadFile 解引用
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		return copyFileWithPerm(path, target, info.Mode().Perm())
	})
}

// copyFileWithPerm 与 cpp_eval.go 中的 copyFile 区别在于保留源文件权限位。
// 用于 repo_level 工作区拷贝，需要保留例如 build 脚本的可执行权限。
func copyFileWithPerm(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
