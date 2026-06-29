package web

import (
	"path/filepath"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestDefaultBuildProfileFastEvalUsesAppDockerfile(t *testing.T) {
	profile, err := defaultBuildProfile("eval", DockerConfig{EvalImageName: "utbench:test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Dockerfile != "Dockerfile.app" {
		t.Fatalf("expected fast rebuild Dockerfile.app, got %s", profile.Dockerfile)
	}
	if profile.BuildArgs["BASE_IMAGE"] != "utbench:test" {
		t.Fatalf("expected BASE_IMAGE build arg, got %#v", profile.BuildArgs)
	}
}

func TestSuppressRuntimeLogLine(t *testing.T) {
	noisy := "INFO  2026-05-14T07:27:09 +0ms service=bus type=message.part.delta publishing"
	if !shouldSuppressRuntimeLogLine(noisy) {
		t.Fatalf("expected noisy bus delta log to be suppressed")
	}
	normal := "INFO  2026-05-14T07:27:09 run completed"
	if shouldSuppressRuntimeLogLine(normal) {
		t.Fatalf("expected normal log to be kept")
	}
}

func TestDockerPathMapperMapsDatasetAndArtifacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "go-ut-bench")
	datasets := filepath.Join(filepath.Dir(root), "datasets")
	mapper := newDockerPathMapper(root, contracts.RunSpec{DatasetRoot: datasets})
	if got := mapper(filepath.Join(datasets, "go", "sample.go")); got != "/app/datasets/go/sample.go" {
		t.Fatalf("unexpected dataset path: %s", got)
	}
	if got := mapper(filepath.Join(root, "artifacts", "runs", "r1", "generated", "test.go")); got != "/app/artifacts/runs/r1/generated/test.go" {
		t.Fatalf("unexpected artifact path: %s", got)
	}
}
