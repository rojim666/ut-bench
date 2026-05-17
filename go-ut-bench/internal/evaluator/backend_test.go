package evaluator

import "testing"

func TestDockerBackendUsesShellEntrypoint(t *testing.T) {
	backend := &DockerBackend{
		Image:           "utbench:test",
		NetworkDisabled: true,
		CPU:             "2",
		Memory:          "4g",
	}

	args := backend.dockerRunArgs(`C:\tmp\work dir`, []string{"mvn", "test", "-Dname=a b"}, []string{"A=B"})

	entrypointIdx := indexOfArg(args, "--entrypoint")
	imageIdx := indexOfArg(args, "utbench:test")
	if entrypointIdx < 0 {
		t.Fatalf("missing --entrypoint in docker args: %#v", args)
	}
	if entrypointIdx+1 >= len(args) || args[entrypointIdx+1] != "/bin/sh" {
		t.Fatalf("expected /bin/sh entrypoint, got args: %#v", args)
	}
	if imageIdx < 0 {
		t.Fatalf("missing image in docker args: %#v", args)
	}
	if !containsArgWithSuffix(args, ":/utbench-cache/m2") {
		t.Fatalf("missing shared Maven cache mount: %#v", args)
	}
	if entrypointIdx > imageIdx {
		t.Fatalf("--entrypoint must be before image: %#v", args)
	}
	if imageIdx+2 >= len(args) || args[imageIdx+1] != "-c" {
		t.Fatalf("expected shell command after image, got args: %#v", args)
	}
	if args[imageIdx+2] != "mvn test '-Dname=a b'" {
		t.Fatalf("unexpected shell command: %q", args[imageIdx+2])
	}
	if containsArg(args[imageIdx+1:], "/bin/sh") {
		t.Fatalf("shell path should be provided as entrypoint, not image argument: %#v", args)
	}
}

func indexOfArg(args []string, want string) int {
	for i, arg := range args {
		if arg == want {
			return i
		}
	}
	return -1
}

func containsArg(args []string, want string) bool {
	return indexOfArg(args, want) >= 0
}

func containsArgWithSuffix(args []string, suffix string) bool {
	for _, arg := range args {
		if len(arg) >= len(suffix) && arg[len(arg)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}
