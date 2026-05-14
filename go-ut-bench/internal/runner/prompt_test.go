package runner

import (
	"strings"
	"testing"
)

func TestPromptForbidsRuntimeEnvironmentMutation(t *testing.T) {
	req := PromptRequest{
		Mode:       PromptModeFullFile,
		Language:   "python",
		SamplePath: "boundary_000.py",
		SourceCode: "def task_func(x):\n    return x + 1\n",
	}

	got := BuildPrompt(req)
	if !strings.Contains(got, "Do not install dependencies") {
		t.Fatalf("prompt should forbid installing dependencies:\n%s", got)
	}
	if !strings.Contains(got, "mutate the runtime environment") {
		t.Fatalf("prompt should forbid runtime environment mutation:\n%s", got)
	}
}
