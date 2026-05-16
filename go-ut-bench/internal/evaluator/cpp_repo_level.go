package evaluator

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const cppRepoLevelCMakeTemplate = `cmake_minimum_required(VERSION 3.16)
project(utbench_cpp_repo_eval LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_POLICY_DEFAULT_CMP0077 NEW)

set(BUILD_TESTING OFF CACHE BOOL "" FORCE)
set(CLI11_WARNINGS_AS_ERRORS OFF CACHE BOOL "" FORCE)
set(CLI11_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(CLI11_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(CLI11_BUILD_EXAMPLES_JSON OFF CACHE BOOL "" FORCE)
set(CLI11_BUILD_DOCS OFF CACHE BOOL "" FORCE)
set(CLI11_SINGLE_FILE_TESTS OFF CACHE BOOL "" FORCE)
set(CLI11_INSTALL OFF CACHE BOOL "" FORCE)
set(MAGIC_ENUM_OPT_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(MAGIC_ENUM_OPT_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(MAGIC_ENUM_OPT_INSTALL OFF CACHE BOOL "" FORCE)
set(FMT_DOC OFF CACHE BOOL "" FORCE)
set(FMT_INSTALL OFF CACHE BOOL "" FORCE)
set(FMT_TEST OFF CACHE BOOL "" FORCE)
set(FMT_FUZZ OFF CACHE BOOL "" FORCE)
set(FMT_CUDA_TEST OFF CACHE BOOL "" FORCE)
set(FMT_WERROR OFF CACHE BOOL "" FORCE)
set(FMT_PEDANTIC OFF CACHE BOOL "" FORCE)

find_package(GTest REQUIRED)

add_subdirectory("%s" "%s" EXCLUDE_FROM_ALL)

%s

add_executable(test_runner
  "%s"
%s
)
target_include_directories(test_runner PRIVATE
%s
)
target_link_libraries(test_runner PRIVATE GTest::gtest_main)

if(NOT UTBENCH_MUTATION_BUILD)
  target_compile_options(test_runner PRIVATE --coverage -fprofile-arcs -ftest-coverage -O0 -g)
  target_link_options(test_runner PRIVATE --coverage)
  target_link_libraries(test_runner PRIVATE gcov)
endif()

set(_utbench_link_candidates
  fmt::fmt
  fmt
  fmt::fmt-header-only
  fmt-header-only
  CLI11::CLI11
  CLI11
  magic_enum::magic_enum
  magic_enum
)
foreach(_utbench_target IN LISTS _utbench_link_candidates)
  if(TARGET ${_utbench_target})
    target_link_libraries(test_runner PRIVATE ${_utbench_target})
  endif()
endforeach()

enable_testing()
add_test(NAME AllTests COMMAND test_runner)
`

func prepareCppRepoLevelWorkspace(testPath, samplePath string) (workdir, testFileName, sourceBase, sourceStem, targetFile, errMsg string) {
	meta := loadRepoLevelMeta(samplePath)
	if meta == nil {
		return "", "", "", "", "", "repo_level sample missing metadata"
	}
	if strings.TrimSpace(meta.WorkspaceRoot) == "" {
		return "", "", "", "", "", "repo_level workspace_root not set in metadata"
	}
	if strings.TrimSpace(meta.TargetFile) == "" {
		return "", "", "", "", "", "repo_level target_file not set in metadata"
	}
	if _, err := os.Stat(meta.WorkspaceRoot); err != nil {
		return "", "", "", "", "", "repo_level workspace not found: " + meta.WorkspaceRoot
	}

	testSource, err := os.ReadFile(testPath)
	if err != nil {
		return "", "", "", "", "", fmt.Sprintf("failed to read generated test: %s", err)
	}
	targetAbs := filepath.Join(meta.WorkspaceRoot, filepath.FromSlash(meta.TargetFile))
	sourceData, err := os.ReadFile(targetAbs)
	if err != nil {
		return "", "", "", "", "", fmt.Sprintf("failed to read source: %s", err)
	}

	sourceBase = filepath.Base(meta.TargetFile)
	sourceStem = strings.TrimSuffix(sourceBase, filepath.Ext(sourceBase))
	testFileName = sourceStem + "_generated_test.cpp"
	targetFile = filepath.ToSlash(meta.TargetFile)
	testSource = rewriteCppRepoGeneratedTestIncludes(stripRedundantDefinitions(testSource, sourceData), targetFile)
	targetIncluded := cppRepoGeneratedTestIncludesTarget(testSource, targetFile)
	buildTargetObject := !targetIncluded && !cppSourceUsesModules(sourceData)

	tmpdir, err := os.MkdirTemp("", "utbench_cpp_repo_eval_")
	if err != nil {
		return "", "", "", "", "", fmt.Sprintf("failed to create temp dir: %s", err)
	}

	skip := func(rel string) bool {
		relSlash := filepath.ToSlash(rel)
		base := filepath.Base(relSlash)
		if strings.HasSuffix(base, ".meta.json") {
			return true
		}
		if isCppRepoGeneratedOrBuildPath(relSlash) {
			return true
		}
		return false
	}
	if err := copyTreeFiltered(meta.WorkspaceRoot, tmpdir, skip); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to copy workspace: %s", err)
	}
	if _, err := os.Stat(filepath.Join(tmpdir, filepath.FromSlash(targetFile))); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("target_file not found in workspace: %s", targetFile)
	}

	harnessDir := filepath.Join(tmpdir, ".utbench_eval")
	buildDir := filepath.Join(harnessDir, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to create C++ repo harness dirs: %s", err)
	}
	if err := os.WriteFile(filepath.Join(harnessDir, testFileName), testSource, 0o644); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to write generated test: %s", err)
	}
	if err := maybeWriteCatchShim(harnessDir, testSource); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to write Catch2 shim: %s", err)
	}

	includeDirs := cppRepoIncludeDirs(tmpdir, targetFile)
	targetBlock, targetObjects := cppRepoTargetObjectCMake(tmpdir, targetFile, includeDirs, buildTargetObject)
	cmake := fmt.Sprintf(
		cppRepoLevelCMakeTemplate,
		cmakePath(tmpdir),
		cmakePath(filepath.Join(buildDir, "project")),
		targetBlock,
		cmakePath(filepath.Join(harnessDir, testFileName)),
		targetObjects,
		formatCMakeIncludeDirs(includeDirs),
	)
	if err := os.WriteFile(filepath.Join(harnessDir, "CMakeLists.txt"), []byte(cmake), 0o644); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to write CMakeLists.txt: %s", err)
	}

	return harnessDir, testFileName, sourceBase, sourceStem, targetFile, ""
}

func cppRepoGeneratedTestIncludesTarget(source []byte, targetFile string) bool {
	targetFile = filepath.ToSlash(targetFile)
	sourceBase := filepath.Base(targetFile)
	includePattern := regexp.MustCompile(`#include\s*[<"]([^>"]+)[>"]`)
	for _, match := range includePattern.FindAllStringSubmatch(string(source), -1) {
		if len(match) < 2 {
			continue
		}
		included := filepath.ToSlash(strings.TrimSpace(match[1]))
		if included == targetFile || included == sourceBase || strings.HasSuffix(included, "/"+targetFile) {
			return true
		}
	}
	return false
}

func cppRepoTargetObjectCMake(projectRoot, targetFile string, includeDirs []string, buildTargetObject bool) (string, string) {
	if !buildTargetObject {
		return "", ""
	}
	block := fmt.Sprintf(`add_library(utbench_target OBJECT "%s")
target_include_directories(utbench_target PRIVATE
%s
)
target_compile_definitions(utbench_target PRIVATE main=utbench_target_main)
if(NOT UTBENCH_MUTATION_BUILD)
  target_compile_options(utbench_target PRIVATE --coverage -fprofile-arcs -ftest-coverage -O0 -g)
endif()
`,
		cmakePath(filepath.Join(projectRoot, filepath.FromSlash(targetFile))),
		formatCMakeIncludeDirs(includeDirs),
	)
	return block, "  $<TARGET_OBJECTS:utbench_target>"
}

func cppSourceUsesModules(source []byte) bool {
	text := stripCppCommentsForModuleScan(string(source))
	moduleRe := regexp.MustCompile(`(?m)^\s*(export\s+)?module\s*([A-Za-z_][A-Za-z0-9_.:]*\s*)?;`)
	return moduleRe.MatchString(text)
}

func stripCppCommentsForModuleScan(text string) string {
	blockRe := regexp.MustCompile(`(?s)/\*.*?\*/`)
	text = blockRe.ReplaceAllString(text, "")
	lineRe := regexp.MustCompile(`(?m)//.*$`)
	return lineRe.ReplaceAllString(text, "")
}

func maybeWriteCatchShim(harnessDir string, source []byte) error {
	if !cppGeneratedTestUsesCatch(source) {
		return nil
	}
	if err := os.WriteFile(filepath.Join(harnessDir, "catch.hpp"), []byte(cppCatchShimHeader), 0o644); err != nil {
		return err
	}
	catch2Dir := filepath.Join(harnessDir, "catch2")
	if err := os.MkdirAll(catch2Dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"catch.hpp", "catch_test_macros.hpp"} {
		if err := os.WriteFile(filepath.Join(catch2Dir, name), []byte(`#pragma once
#include "../catch.hpp"
`), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func cppGeneratedTestUsesCatch(source []byte) bool {
	text := string(source)
	return strings.Contains(text, "#include \"catch.hpp\"") ||
		strings.Contains(text, "#include <catch.hpp>") ||
		strings.Contains(text, "#include \"catch2/catch.hpp\"") ||
		strings.Contains(text, "#include <catch2/catch.hpp>") ||
		strings.Contains(text, "#include \"catch2/catch_test_macros.hpp\"") ||
		strings.Contains(text, "#include <catch2/catch_test_macros.hpp>")
}

const cppCatchShimHeader = `#pragma once
#include <gtest/gtest.h>

#define UTBENCH_CATCH_CONCAT_INNER(a, b) a##b
#define UTBENCH_CATCH_CONCAT(a, b) UTBENCH_CATCH_CONCAT_INNER(a, b)

#define TEST_CASE(name, tags) TEST(UtbenchCatchShim, UTBENCH_CATCH_CONCAT(Case_, __LINE__))
#define SECTION(name) if (bool UTBENCH_CATCH_CONCAT(_utbench_section_, __LINE__) = true)

#define CHECK(expr) EXPECT_TRUE((expr))
#define REQUIRE(expr) ASSERT_TRUE((expr))
#define CHECK_FALSE(expr) EXPECT_FALSE((expr))
#define REQUIRE_FALSE(expr) ASSERT_FALSE((expr))

#define CHECK_EQ(a, b) EXPECT_EQ((a), (b))
#define CHECK_NE(a, b) EXPECT_NE((a), (b))
#define CHECK_LT(a, b) EXPECT_LT((a), (b))
#define CHECK_LE(a, b) EXPECT_LE((a), (b))
#define CHECK_GT(a, b) EXPECT_GT((a), (b))
#define CHECK_GE(a, b) EXPECT_GE((a), (b))

#define REQUIRE_EQ(a, b) ASSERT_EQ((a), (b))
#define REQUIRE_NE(a, b) ASSERT_NE((a), (b))
#define REQUIRE_LT(a, b) ASSERT_LT((a), (b))
#define REQUIRE_LE(a, b) ASSERT_LE((a), (b))
#define REQUIRE_GT(a, b) ASSERT_GT((a), (b))
#define REQUIRE_GE(a, b) ASSERT_GE((a), (b))

#define CHECK_THROWS(expr) EXPECT_ANY_THROW(expr)
#define REQUIRE_THROWS(expr) ASSERT_ANY_THROW(expr)
#define CHECK_NOTHROW(expr) EXPECT_NO_THROW(expr)
#define REQUIRE_NOTHROW(expr) ASSERT_NO_THROW(expr)
#define CHECK_THROWS_AS(expr, exc) EXPECT_THROW((expr), exc)
#define REQUIRE_THROWS_AS(expr, exc) ASSERT_THROW((expr), exc)

#define INFO(msg) SCOPED_TRACE(msg)
`

func isCppRepoGeneratedOrBuildPath(rel string) bool {
	rel = strings.ToLower(filepath.ToSlash(rel))
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		switch part {
		case ".git", ".utbench_eval", "build", "cmake-build-debug", "cmake-build-release", "out", ".cache":
			return true
		}
		if strings.HasPrefix(part, "cmake-build-") {
			return true
		}
	}
	return strings.HasPrefix(rel, ".utbench/")
}

func rewriteCppRepoGeneratedTestIncludes(source []byte, targetFile string) []byte {
	targetFile = filepath.ToSlash(targetFile)
	sourceBase := filepath.Base(targetFile)
	text := string(source)

	replacements := map[string]string{
		`#include "source.cpp"`:         `#include "` + targetFile + `"`,
		`#include <source.cpp>`:         `#include "` + targetFile + `"`,
		`#include "` + sourceBase + `"`: `#include "` + targetFile + `"`,
		`#include <` + sourceBase + `>`: `#include "` + targetFile + `"`,
	}
	for old, newValue := range replacements {
		text = strings.ReplaceAll(text, old, newValue)
	}

	absInclude := regexp.MustCompile(`#include\s+"(?:/app/|[A-Za-z]:[/\\])[^"]*` + regexp.QuoteMeta(sourceBase) + `"`)
	text = absInclude.ReplaceAllString(text, `#include "`+targetFile+`"`)
	return []byte(text)
}

func cppRepoIncludeDirs(projectRoot, targetFile string) []string {
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(path)
		if path == "." || path == "" {
			return
		}
		if seen[path] {
			return
		}
		seen[path] = true
	}
	add(projectRoot)
	add(filepath.Join(projectRoot, "include"))
	add(filepath.Join(projectRoot, "src"))
	add(filepath.Join(projectRoot, filepath.Dir(filepath.FromSlash(targetFile))))

	_ = filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		base := strings.ToLower(d.Name())
		if isCppRepoGeneratedOrBuildPath(pathRelBestEffort(projectRoot, path)) {
			return filepath.SkipDir
		}
		if base == "include" || base == "src" || base == "fuzz" || base == "single-include" {
			add(path)
		}
		return nil
	})

	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func pathRelBestEffort(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

func formatCMakeIncludeDirs(paths []string) string {
	lines := make([]string, 0, len(paths))
	for _, path := range paths {
		lines = append(lines, `  "`+cmakePath(path)+`"`)
	}
	return strings.Join(lines, "\n")
}

func cmakePath(path string) string {
	return strings.ReplaceAll(filepath.ToSlash(filepath.Clean(path)), `"`, `\"`)
}

func cppCompileCheckRepoLevel(workdir string) (bool, string) {
	return cppCompileCheck(workdir)
}

func executeCppTestsRepoLevel(workdir string, timeoutSeconds int) (bool, string, int) {
	buildDir := filepath.Join(workdir, "build")
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultTestTimeoutSeconds
	}
	runCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	started := time.Now()
	output, err := runCommandWithProcessGroupKill(runCtx, "./test_runner", nil, buildDir, nil)
	latency := int(time.Since(started).Milliseconds())
	if runCtx.Err() != nil {
		return false, fmt.Sprintf("cpp test timed out after %ds", timeoutSeconds), latency
	}
	if err == nil {
		return true, string(output), latency
	}
	return false, trimErr(string(output), 4000), latency
}

func collectCppCoverageRepoLevel(workdir, testFileName string) (float64, float64, string) {
	return collectCppCoverage(workdir, testFileName)
}

func collectCppMutationRepoLevel(ctx context.Context, workdir string, timeoutSeconds int, testPassRate *float64, testPassed, testTotal int) (float64, mutationStats, string) {
	if timeoutSeconds <= 0 || timeoutSeconds > CppMutationTimeoutSeconds {
		timeoutSeconds = CppMutationTimeoutSeconds
	}

	minPassRate := GetMinPassRateForTool("mull")
	passed := 0
	total := 0
	if testPassed > 0 || testTotal > 0 {
		passed = testPassed
		total = testTotal
	} else if testPassRate != nil {
		total = 100
		passed = int(math.Round(*testPassRate * float64(total)))
		if passed > total {
			passed = total
		}
	}

	checkResult := CheckTestPassRate(passed, total, "Mull", minPassRate)
	if !checkResult.ShouldRun {
		return 0, mutationStats{}, checkResult.Message
	}

	mullRunner := findMullRunner()
	if mullRunner == "" {
		return 0, mutationStats{}, "Mull not installed. Install via GitHub Releases: https://github.com/mull-project/mull/releases or use the Docker image."
	}
	mullFrontend := findMullFrontend()
	if mullFrontend == "" {
		return 0, mutationStats{}, "mull-ir-frontend-19 not found"
	}
	clangXX := findCommandCandidate("clang++-19", "clang++")
	if clangXX == "" {
		return 0, mutationStats{}, "clang++-19 not found"
	}
	clangC := findCommandCandidate("clang-19", "clang")

	mullConfigPath := filepath.Join(workdir, "mull.yml")
	if err := os.WriteFile(mullConfigPath, []byte(cppRepoMullConfig()), 0o644); err != nil {
		return 0, mutationStats{}, "failed to write mull.yml: " + err.Error()
	}

	buildDir := filepath.Join(workdir, "build_mull")
	if err := os.RemoveAll(buildDir); err != nil {
		return 0, mutationStats{}, "failed to reset Mull build dir: " + err.Error()
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return 0, mutationStats{}, "failed to create Mull build dir: " + err.Error()
	}

	commonFlags := strings.Join([]string{
		"-O0",
		"-g",
		"-grecord-command-line",
		"-fno-inline",
		"-fno-omit-frame-pointer",
		"-fpass-plugin=" + mullFrontend,
	}, " ")
	cmakeArgs := []string{
		"..",
		"-DCMAKE_BUILD_TYPE=Debug",
		"-DUTBENCH_MUTATION_BUILD=ON",
		"-DCMAKE_CXX_COMPILER=" + clangXX,
		"-DCMAKE_CXX_FLAGS=" + commonFlags,
	}
	if clangC != "" {
		cmakeArgs = append(cmakeArgs, "-DCMAKE_C_COMPILER="+clangC)
	}

	configureCtx, cancelConfigure := context.WithTimeout(ctx, 180*time.Second)
	configureOut, configureErr := runCommandWithProcessGroupKill(configureCtx, "cmake", cmakeArgs, buildDir, nil)
	configureCtxErr := configureCtx.Err()
	cancelConfigure()
	if configureCtxErr == context.DeadlineExceeded {
		return 0, mutationStats{}, "cmake for Mull timed out after 180s"
	}
	if configureCtxErr != nil {
		return 0, mutationStats{}, "cmake for Mull canceled: " + configureCtxErr.Error()
	}
	if configureErr != nil {
		return 0, mutationStats{}, "cmake for Mull failed\n" + trimErr(string(configureOut), 4000)
	}

	buildCtx, cancelBuild := context.WithTimeout(ctx, 300*time.Second)
	buildOut, buildErr := runCommandWithProcessGroupKill(buildCtx, "make", []string{"-j2", "test_runner"}, buildDir, nil)
	buildCtxErr := buildCtx.Err()
	cancelBuild()
	if buildCtxErr == context.DeadlineExceeded {
		return 0, mutationStats{}, "make for Mull timed out after 300s"
	}
	if buildCtxErr != nil {
		return 0, mutationStats{}, "make for Mull canceled: " + buildCtxErr.Error()
	}
	if buildErr != nil {
		return 0, mutationStats{}, "make for Mull failed\n" + trimErr(string(buildOut), 4000)
	}

	binaryPath := filepath.Join(buildDir, "test_runner")
	if _, err := os.Stat(binaryPath); err != nil {
		return 0, mutationStats{}, "mull executable not found after build: " + binaryPath
	}

	runCtx, cancelRun := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancelRun()
	mullEnv := append(os.Environ(),
		"LD_LIBRARY_PATH=/usr/lib/llvm-19/lib:/usr/lib/x86_64-linux-gnu:/lib/x86_64-linux-gnu:/usr/lib64:"+os.Getenv("LD_LIBRARY_PATH"),
	)
	mullOut, mullErr := runCommandWithProcessGroupKill(runCtx, mullRunner, []string{"--allow-surviving", binaryPath}, workdir, mullEnv)
	if runCtx.Err() == context.DeadlineExceeded {
		return 0, mutationStats{}, fmt.Sprintf("mull timed out after %ds", timeoutSeconds)
	}
	if mullOutputReportsNoMutants(string(mullOut)) {
		return 0, mutationStats{}, ""
	}

	stats, parseErr := parseMullOutput(string(mullOut))
	if parseErr != "" {
		return 0, stats, formatMullError("mull error", mullErr, mullOut)
	}
	if stats.Total <= 0 {
		return 0, stats, "mull found zero mutants for compiled C++ repo target"
	}

	score := round(float64(stats.Killed)/float64(stats.Total), 6)
	return score, stats, ""
}

func cppRepoMullConfig() string {
	return `excludePaths:
  - ".*\\.h$"
  - ".*\\.hpp$"
  - "^/usr/.*"
  - ".*googletest.*"
  - ".*/\\.utbench_eval/.*"
  - ".*/build_mull/.*"

timeout: 30000
`
}

func findCommandCandidate(candidates ...string) string {
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}
