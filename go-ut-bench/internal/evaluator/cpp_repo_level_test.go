package evaluator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestCppRepoLevelPrepareWorkspaceCreatesHarness(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "cpp", "cpp_code_files_repo_level", "oss", "fmt")
	includeDir := filepath.Join(repo, "include", "fmt")
	srcDir := filepath.Join(repo, "src")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmake := `cmake_minimum_required(VERSION 3.16)
project(fmt LANGUAGES CXX)
add_library(fmt INTERFACE)
add_library(fmt::fmt ALIAS fmt)
target_include_directories(fmt INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)
`
	if err := os.WriteFile(filepath.Join(repo, "CMakeLists.txt"), []byte(cmake), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(includeDir, "format.h"), []byte("#pragma once\nnamespace fmt { inline const char* hello() { return \"hello\"; } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(srcDir, "fmt.cc")
	if err := os.WriteFile(sourcePath, []byte("#include \"fmt/format.h\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generatedTest := filepath.Join(root, "fmt_test.cpp")
	testSource := `#include <gtest/gtest.h>
#include "fmt/format.h"

TEST(FmtRepoLevel, IncludesHeader) {
  EXPECT_STREQ("hello", fmt::hello());
}
`
	if err := os.WriteFile(generatedTest, []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := (&CppEvaluator{}).PrepareWorkspace(contracts.GeneratedCase{
		Language:          "cpp",
		SampleID:          "src_fmt",
		SamplePath:        sourcePath,
		GeneratedTestPath: generatedTest,
		Success:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(ws.Workdir))

	if ws.Extra["isRepoLevel"] != "true" {
		t.Fatalf("expected repo_level workspace, got %+v", ws.Extra)
	}
	if ws.Extra["targetFile"] != "src/fmt.cc" {
		t.Fatalf("target file = %q", ws.Extra["targetFile"])
	}
	if ws.TestPath != "fmt_generated_test.cpp" {
		t.Fatalf("test path = %q", ws.TestPath)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ws.Workdir), "src", "fmt.cc")); err != nil {
		t.Fatalf("project source was not copied: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(ws.Workdir, "CMakeLists.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cmakeText := string(raw)
	for _, want := range []string{"add_subdirectory", "add_library(utbench_target OBJECT", "target_compile_definitions(utbench_target PRIVATE main=utbench_target_main)", "fmt::fmt", "/include", "/src"} {
		if !strings.Contains(cmakeText, want) {
			t.Fatalf("CMakeLists.txt missing %q:\n%s", want, cmakeText)
		}
	}
}

func TestCppRepoLevelPrepareWorkspaceSkipsObjectForModuleUnit(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "cpp", "cpp_code_files_repo_level", "oss", "fmt")
	includeDir := filepath.Join(repo, "include", "fmt")
	srcDir := filepath.Join(repo, "src")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmake := `cmake_minimum_required(VERSION 3.16)
project(fmt LANGUAGES CXX)
add_library(fmt INTERFACE)
add_library(fmt::fmt ALIAS fmt)
target_include_directories(fmt INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)
`
	if err := os.WriteFile(filepath.Join(repo, "CMakeLists.txt"), []byte(cmake), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(includeDir, "format.h"), []byte("#pragma once\nnamespace fmt { inline const char* hello() { return \"hello\"; } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(srcDir, "fmt.cc")
	if err := os.WriteFile(sourcePath, []byte("module;\nexport module fmt;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generatedTest := filepath.Join(root, "fmt_test.cpp")
	testSource := `#include <gtest/gtest.h>
#include "fmt/format.h"
TEST(FmtRepoLevel, IncludesHeader) { EXPECT_STREQ("hello", fmt::hello()); }
`
	if err := os.WriteFile(generatedTest, []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := (&CppEvaluator{}).PrepareWorkspace(contracts.GeneratedCase{
		Language:          "cpp",
		SampleID:          "src_fmt",
		SamplePath:        sourcePath,
		GeneratedTestPath: generatedTest,
		Success:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(ws.Workdir))

	raw, err := os.ReadFile(filepath.Join(ws.Workdir, "CMakeLists.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "add_library(utbench_target OBJECT") {
		t.Fatalf("module unit should not be compiled as plain object:\n%s", raw)
	}
}

func TestCppRepoLevelPrepareWorkspaceWritesCatchShim(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "cpp", "cpp_code_files_repo_level", "oss", "CLI11")
	includeDir := filepath.Join(repo, "include", "CLI")
	bookDir := filepath.Join(repo, "book", "code")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmake := `cmake_minimum_required(VERSION 3.16)
project(CLI11 LANGUAGES CXX)
add_library(CLI11 INTERFACE)
add_library(CLI11::CLI11 ALIAS CLI11)
target_include_directories(CLI11 INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)
`
	if err := os.WriteFile(filepath.Join(repo, "CMakeLists.txt"), []byte(cmake), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(includeDir, "CLI.hpp"), []byte("#pragma once\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(bookDir, "flags.cpp")
	if err := os.WriteFile(sourcePath, []byte("int main(int, char**) { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generatedTest := filepath.Join(root, "flags_test.cpp")
	testSource := `#include "CLI/CLI.hpp"
#include "catch.hpp"
TEST_CASE("flag defaults", "[flags]") {
  CHECK(true);
}
`
	if err := os.WriteFile(generatedTest, []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := (&CppEvaluator{}).PrepareWorkspace(contracts.GeneratedCase{
		Language:          "cpp",
		SampleID:          "book_code_flags",
		SamplePath:        sourcePath,
		GeneratedTestPath: generatedTest,
		Success:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(ws.Workdir))

	for _, rel := range []string{"catch.hpp", filepath.Join("catch2", "catch.hpp"), filepath.Join("catch2", "catch_test_macros.hpp")} {
		if _, err := os.Stat(filepath.Join(ws.Workdir, rel)); err != nil {
			t.Fatalf("missing Catch shim %s: %v", rel, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(ws.Workdir, "catch.hpp"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "#define TEST_CASE") || !strings.Contains(string(raw), "#define CHECK(expr)") {
		t.Fatalf("unexpected Catch shim:\n%s", raw)
	}
}
