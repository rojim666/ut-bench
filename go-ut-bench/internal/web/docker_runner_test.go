package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"
	"go-ut-bench/internal/orchestrator"
)

func TestBuildDockerRunArgsUsesMountedSource(t *testing.T) {
	spec := contracts.RunSpec{
		RunID:     "run-1",
		Models:    []string{"deepseek"},
		Languages: []string{"python"},
	}
	cfg := DockerConfig{EvalImageName: "utbench:latest", ProjectRoot: "/repo"}

	args := buildDockerRunArgs(spec, orchestrator.Options{}, cfg)
	joined := strings.Join(args, " ")

	mustContain(t, joined, "-v /repo/datasets:/app/datasets")
	mustContain(t, joined, "-v /repo/artifacts:/app/artifacts")
	mustContain(t, joined, "utbench:latest")
	mustContain(t, joined, "--output-root /app/artifacts")
	mustContain(t, joined, "--models deepseek")
}

func TestBuildDockerRunArgsUsesSpecDatasetRootMount(t *testing.T) {
	spec := contracts.RunSpec{
		RunID:       "run-1",
		Models:      []string{"deepseek"},
		Languages:   []string{"go"},
		DatasetRoot: "../datasets",
	}
	cfg := DockerConfig{EvalImageName: "utbench:latest", ProjectRoot: "/repo/go-ut-bench"}

	args := buildDockerRunArgs(spec, orchestrator.Options{}, cfg)
	joined := strings.Join(args, " ")

	mustContain(t, joined, "-v /repo/datasets:/app/datasets")
}

func TestBuildDockerEvaluateArgsUsesSourceRunManifest(t *testing.T) {
	spec := contracts.RunSpec{
		RunID:           "run-1",
		MutationEnabled: true,
		MutationTimeout: 120,
		MutationPolicy:  "warn",
		Workers:         4,
	}
	cfg := DockerConfig{EvalImageName: "utbench:latest", ProjectRoot: "/repo"}

	args := buildDockerRunArgs(spec, orchestrator.Options{
		Phase:       "evaluate",
		SourceRunID: "source-run",
	}, cfg)
	joined := strings.Join(args, " ")

	mustContain(t, joined, "--run-id run-1")
	mustContain(t, joined, "--manifest /app/artifacts/runs/source-run/generated/generated_manifest.json")
	mustContain(t, joined, "--mutation-enabled")
	mustContain(t, joined, "--mutation-timeout 120")
	mustNotContain(t, joined, "--workers")
}

func TestBuildDockerRunArgsAppliesResourceLimits(t *testing.T) {
	spec := contracts.RunSpec{
		RunID:     "run-1",
		Models:    []string{"deepseek"},
		Languages: []string{"python"},
	}
	cfg := DockerConfig{
		EvalImageName: "utbench:latest",
		ProjectRoot:   "/repo",
		EvalMemory:    "4g",
		EvalCPUs:      "2",
	}

	args := buildDockerRunArgs(spec, orchestrator.Options{}, cfg)
	joined := strings.Join(args, " ")

	mustContain(t, joined, "--memory 4g")
	mustContain(t, joined, "--memory-swap 4g")
	mustContain(t, joined, "--cpus 2")
}

func TestBuildDockerRunArgsPassesReuseGeneratedWithDBPath(t *testing.T) {
	spec := contracts.RunSpec{
		RunID:          "run-1",
		Models:         []string{"deepseek"},
		Languages:      []string{"go"},
		ReuseGenerated: true,
	}
	cfg := DockerConfig{EvalImageName: "utbench:latest", ProjectRoot: "/repo"}

	args := buildDockerRunArgs(spec, orchestrator.Options{}, cfg)
	joined := strings.Join(args, " ")

	mustContain(t, joined, "--reuse-generated")
	mustContain(t, joined, "--db-path /app/storage/utbench.db")
}

func TestBuildDockerEvaluateArgsMapsWindowsRelativeManifest(t *testing.T) {
	spec := contracts.RunSpec{RunID: "run-1"}
	cfg := DockerConfig{EvalImageName: "utbench:latest", ProjectRoot: `F:\repo\go-ut-bench`}

	args := buildDockerRunArgs(spec, orchestrator.Options{
		Phase:        "evaluate",
		ManifestPath: `artifacts\runs\source-run\generated\generated_manifest.docker.json`,
	}, cfg)
	joined := strings.Join(args, " ")

	mustContain(t, joined, "--manifest /app/artifacts/runs/source-run/generated/generated_manifest.docker.json")
}

func TestDockerEvaluateOptionsPreparesContainerManifest(t *testing.T) {
	root := t.TempDir()
	runID := "run-1"
	manifestDir := filepath.Join(root, "artifacts", "runs", runID, "generated")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hostSample := filepath.Join(root, "datasets", "go", "go_code_files_repo_level", "oss", "demo", "demo.go")
	manifest := contracts.GeneratedManifest{
		SchemaVersion: contracts.SchemaVersion,
		RunID:         runID,
		CreatedAtUTC:  time.Now().UTC(),
		Spec: contracts.RunSpec{
			RunID:       runID,
			DatasetRoot: filepath.Join(root, "datasets"),
			OutputRoot:  filepath.Join(root, "artifacts"),
			ConfigPath:  filepath.Join(root, "configs", "models.yaml"),
		},
		Cases: []contracts.GeneratedCase{{
			Model:             "deepseek",
			Language:          "go",
			SampleID:          "demo",
			SamplePath:        hostSample,
			GeneratedTestPath: filepath.Join(root, "artifacts", "runs", runID, "generated", "tests", "demo_test.go"),
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(manifestDir, "generated_manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}

	m := &RunManager{dockerCfg: DockerConfig{ProjectRoot: root}}
	opts, err := m.dockerEvaluateOptions(manifest.Spec, orchestrator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.ToSlash(opts.ManifestPath), "/generated_manifest.docker.json") {
		t.Fatalf("expected docker manifest path, got %q", opts.ManifestPath)
	}

	dockerManifest, err := contracts.ReadGeneratedManifest(opts.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := dockerManifest.Cases[0].SamplePath; got != "/app/datasets/go/go_code_files_repo_level/oss/demo/demo.go" {
		t.Fatalf("expected container sample path, got %q", got)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected %q not to contain %q", haystack, needle)
	}
}
