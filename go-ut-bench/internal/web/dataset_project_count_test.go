package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCountRepoLevelProjectSamplesMatchesSyntheticModules(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "datasets", "java", "java_code_files_repo_level", "oss", "demo")
	mainDir := filepath.Join(projectRoot, "src", "main", "java", "demo")
	testDir := filepath.Join(projectRoot, "src", "test", "java", "demo")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(projectRoot, "pom.xml"), "<project/>")
	write(filepath.Join(mainDir, "Demo.java"), "package demo;\npublic class Demo {}\n")
	write(filepath.Join(testDir, "DemoTest.java"), "package demo;\npublic class DemoTest {}\n")

	if got := countRepoLevelProjectSamples(projectRoot, "java"); got != 1 {
		t.Fatalf("expected only production source to be counted, got %d", got)
	}
}
