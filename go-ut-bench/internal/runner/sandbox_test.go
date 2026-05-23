package runner

import (
	"testing"
)

func TestResolveDockerWorkspaceMountUsesHostOutputRootOverride(t *testing.T) {
	t.Setenv("UTBENCH_SANDBOX_HOST_OUTPUT_ROOT", "/host/artifacts")
	req := SandboxRunRequest{
		Workspace:           "/app/artifacts/runs/run_1/agent_workspaces/opencode/python/sample_001",
		ContainerOutputRoot: "/app/artifacts",
	}
	got, err := resolveDockerWorkspaceMount(req)
	if err != nil {
		t.Fatalf("resolveDockerWorkspaceMount returned error: %v", err)
	}
	want := "/host/artifacts/runs/run_1/agent_workspaces/opencode/python/sample_001"
	if got != want {
		t.Fatalf("unexpected mount source: got=%s want=%s", got, want)
	}
}

func TestResolveDockerWorkspaceMountFailsFastForContainerLocalPathWithoutHostOverride(t *testing.T) {
	req := SandboxRunRequest{
		Workspace:           "/app/artifacts/runs/run_1/agent_workspaces/opencode/python/sample_001",
		ContainerOutputRoot: "/app/artifacts",
	}
	_, err := resolveDockerWorkspaceMount(req)
	if err == nil {
		t.Fatalf("expected error for container-local workspace without host override")
	}
}

func TestMapContainerPathToDockerHostMapsDatasetPath(t *testing.T) {
	t.Setenv("UTBENCH_SANDBOX_CONTAINER_DATASET_ROOT", "/app/datasets")
	t.Setenv("UTBENCH_SANDBOX_HOST_DATASET_ROOT", "/repo/datasets")

	got, ok := mapContainerPathToDockerHost("/app/datasets/python/sample.py")
	if !ok {
		t.Fatalf("expected dataset path to be mapped")
	}
	want := "/repo/datasets/python/sample.py"
	if got != want {
		t.Fatalf("unexpected mapped path: got=%s want=%s", got, want)
	}
}

func TestMapDockerHostPathToContainerMapsWindowsOutputRoot(t *testing.T) {
	t.Setenv("UTBENCH_SANDBOX_HOST_OUTPUT_ROOT", "C:/projectpractice1/ut-bench/go-ut-bench/artifacts")
	t.Setenv("UTBENCH_SANDBOX_CONTAINER_OUTPUT_ROOT", "/app/artifacts")

	got, ok := mapDockerHostPathToContainer("C:/projectpractice1/ut-bench/go-ut-bench/artifacts/runs/run_1/agent_workspaces/opencode/python/sample_001")
	if !ok {
		t.Fatalf("expected Windows host path to be mapped")
	}
	want := "/app/artifacts/runs/run_1/agent_workspaces/opencode/python/sample_001"
	if got != want {
		t.Fatalf("unexpected container path: got=%s want=%s", got, want)
	}
}

func TestAgentWorkspaceOutputRootUsesContainerOutputRoot(t *testing.T) {
	t.Setenv("UTBENCH_SANDBOX_HOST_OUTPUT_ROOT", "C:/projectpractice1/ut-bench/go-ut-bench/artifacts")
	t.Setenv("UTBENCH_SANDBOX_CONTAINER_OUTPUT_ROOT", "/app/artifacts")

	got := agentWorkspaceOutputRoot("C:/projectpractice1/ut-bench/go-ut-bench/artifacts")
	if got != "/app/artifacts" {
		t.Fatalf("unexpected output root: got=%s want=/app/artifacts", got)
	}
}

func TestAppendDockerBindMountUsesMountSyntaxForWindowsPath(t *testing.T) {
	args := appendDockerBindMount(nil, "C:/projectpractice1/ut-bench/go-ut-bench/artifacts/runs/run_1/agent_workspaces/opencode/python/sample_001", "/workspace", false)
	if len(args) != 2 || args[0] != "--mount" {
		t.Fatalf("expected --mount syntax, got %+v", args)
	}
	want := "type=bind,source=C:/projectpractice1/ut-bench/go-ut-bench/artifacts/runs/run_1/agent_workspaces/opencode/python/sample_001,target=/workspace"
	if args[1] != want {
		t.Fatalf("unexpected mount spec: got=%s want=%s", args[1], want)
	}
}
