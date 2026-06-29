package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ut-bench/internal/agentconfig"
	"go-ut-bench/internal/contracts"
)

type fakeSandboxRunner struct {
	t             *testing.T
	workspace     string
	exportPayload string
	commands      []string
}

func (f *fakeSandboxRunner) Run(_ context.Context, req SandboxRunRequest) (SandboxRunResult, error) {
	f.commands = append(f.commands, req.Command)
	if !strings.Contains(req.Command, "opencode export") {
		f.t.Fatalf("unexpected sandbox command: %s", req.Command)
	}
	exportPath := filepath.Join(f.workspace, ".utbench", "opencode", "session_export.json")
	if err := os.MkdirAll(filepath.Dir(exportPath), 0o755); err != nil {
		f.t.Fatalf("mkdir export path: %v", err)
	}
	if err := os.WriteFile(exportPath, []byte(f.exportPayload), 0o644); err != nil {
		f.t.Fatalf("write export payload: %v", err)
	}
	return SandboxRunResult{ExitCode: 0}, nil
}

func TestCollectOpenCodeSessionExportUsesExportedUsage(t *testing.T) {
	workRoot := t.TempDir()
	traceDir := filepath.Join(t.TempDir(), "trace")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatalf("mkdir trace dir: %v", err)
	}

	runner := &fakeSandboxRunner{
		t:         t,
		workspace: workRoot,
		exportPayload: `{
			"session": {
				"id": "ses_test",
				"messages": [
					{"role":"assistant","usage":{"input_tokens":1200,"output_tokens":300,"total_tokens":1500}},
					{"role":"assistant","usage":{"input_tokens":800,"output_tokens":200,"total_tokens":1000}}
				]
			}
		}`,
	}

	trace := AgentTrace{
		Framework: "opencode",
		SessionID: "ses_test",
	}

	collectOpenCodeSessionExport(
		context.Background(),
		runner,
		SandboxRunRequest{Workspace: workRoot, DockerImage: "utbench-agent-base:latest"},
		workRoot,
		traceDir,
		"boundary_000",
		&trace,
	)

	if trace.SessionExportError != "" {
		t.Fatalf("unexpected session export error: %s", trace.SessionExportError)
	}
	if trace.SessionExportPath == "" {
		t.Fatalf("expected session export path to be recorded")
	}
	if got, want := trace.UsageSourceDetail, "opencode_session_export"; got != want {
		t.Fatalf("usage source detail = %q, want %q", got, want)
	}
	if got, want := trace.TokenSource, "actual"; got != want {
		t.Fatalf("token source = %q, want %q", got, want)
	}
	if trace.PromptTokens == nil || *trace.PromptTokens != 2000 {
		t.Fatalf("prompt tokens = %v, want 2000", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 500 {
		t.Fatalf("completion tokens = %v, want 500", trace.CompletionTokens)
	}
	if trace.TotalTokens == nil || *trace.TotalTokens != 2500 {
		t.Fatalf("total tokens = %v, want 2500", trace.TotalTokens)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("expected 1 export command, got %d", len(runner.commands))
	}
	if _, err := os.Stat(trace.SessionExportPath); err != nil {
		t.Fatalf("session export file not persisted: %v", err)
	}
}

func TestCollectOpenCodeSessionExportUsesOpenCodeMessageTokensSchema(t *testing.T) {
	workRoot := t.TempDir()
	traceDir := filepath.Join(t.TempDir(), "trace")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatalf("mkdir trace dir: %v", err)
	}

	runner := &fakeSandboxRunner{
		t:         t,
		workspace: workRoot,
		exportPayload: `{
			"info": {"id":"ses_test"},
			"messages": [
				{"info":{"role":"assistant","tokens":{"total":11840,"input":105,"output":52}}},
				{"info":{"role":"assistant","tokens":{"total":14584,"input":651,"output":1310}}},
				{"info":{"role":"assistant","tokens":{"total":14718,"input":136,"output":92}}},
				{"info":{"role":"assistant","tokens":{"total":14861,"input":159,"output":96}}},
				{"info":{"role":"assistant","tokens":{"total":16301,"input":1323,"output":32}}}
			]
		}`,
	}

	trace := AgentTrace{
		Framework:        "opencode",
		SessionID:        "ses_test",
		InteractionCount: 108,
	}

	collectOpenCodeSessionExport(
		context.Background(),
		runner,
		SandboxRunRequest{Workspace: workRoot, DockerImage: "utbench-agent-base:latest"},
		workRoot,
		traceDir,
		"boundary_000",
		&trace,
	)

	if trace.SessionExportError != "" {
		t.Fatalf("unexpected session export error: %s", trace.SessionExportError)
	}
	if got, want := trace.TokenSource, "actual"; got != want {
		t.Fatalf("token source = %q, want %q", got, want)
	}
	if got, want := trace.UsageSourceDetail, "opencode_session_export"; got != want {
		t.Fatalf("usage source detail = %q, want %q", got, want)
	}
	if trace.PromptTokens == nil || *trace.PromptTokens != 2374 {
		t.Fatalf("prompt tokens = %v, want 2374", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 1582 {
		t.Fatalf("completion tokens = %v, want 1582", trace.CompletionTokens)
	}
	if trace.TotalTokens == nil || *trace.TotalTokens != 3956 {
		t.Fatalf("total tokens = %v, want 3956 (prompt+completion, not cumulative sum)", trace.TotalTokens)
	}
	if trace.InteractionCount != 5 {
		t.Fatalf("interaction_count = %d, want 5 from assistant messages", trace.InteractionCount)
	}
}

func TestParseFileWritesFiltersOpenCodeWorkspaceNoise(t *testing.T) {
	got := parseFileWrites(
		[]string{
			"go mod init workspace",
			"go test ./workspace/generated_test.go",
			"cat /workspace/utbench_agent_prompt.md",
		},
		[]string{
			".utbench/xdg-config/opencode/config.json",
			".utbench/xdg-data/opencode/opencode.db",
			"go.mod",
			"generated_test.go",
			"test_runner",
		},
	)

	want := []string{"./workspace/generated_test.go", "generated_test.go"}
	if len(got) != len(want) {
		t.Fatalf("files_written len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("files_written[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestShouldFallbackCLIAgentToModelAPIForMissingTestFile(t *testing.T) {
	req := AgentGenerateRequest{
		Subject: agentconfig.ResolvedSubject{
			Spec: contracts.SubjectSpec{Kind: "cli_agent", Framework: "opencode"},
		},
		Model: modelConfig{Name: "deepseek"},
	}
	trace := AgentTrace{Framework: "opencode"}

	if !shouldFallbackCLIAgentToModelAPI(req, trace, "agent did not produce a test file", nil) {
		t.Fatalf("expected cli agent output miss to fall back to model API")
	}
	if shouldFallbackCLIAgentToModelAPI(req, trace, "agent command failed", errors.New("exit status 1")) {
		t.Fatalf("did not expect generic execution failures to fall back")
	}
	req.Subject.Spec.Kind = "model_api"
	if shouldFallbackCLIAgentToModelAPI(req, trace, "agent did not produce a test file", nil) {
		t.Fatalf("model_api subject should not use cli-agent fallback")
	}
}

func TestParseCodeBuddyJSONOutput(t *testing.T) {
	trace := &AgentTrace{Framework: "codebuddy"}
	stdout := `{"result":"Test file created","session_id":"sess_abc123","usage":{"input_tokens":5000,"output_tokens":1200}}`
	parseCodeBuddyJSONOutput(trace, stdout)

	if trace.SessionID != "sess_abc123" {
		t.Fatalf("session_id = %q, want %q", trace.SessionID, "sess_abc123")
	}
	if trace.PromptTokens == nil || *trace.PromptTokens != 5000 {
		t.Fatalf("prompt_tokens = %v, want 5000", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 1200 {
		t.Fatalf("completion_tokens = %v, want 1200", trace.CompletionTokens)
	}
	if trace.TokenSource != "actual" {
		t.Fatalf("token_source = %q, want %q", trace.TokenSource, "actual")
	}
	if trace.UsageSourceDetail != "codebuddy_json_output" {
		t.Fatalf("usage_source_detail = %q, want %q", trace.UsageSourceDetail, "codebuddy_json_output")
	}
}

func TestParseCodeBuddyJSONOutputKeepsNetAndCacheTokens(t *testing.T) {
	trace := &AgentTrace{Framework: "codebuddy"}
	stdout := `{"result":"Done","session_id":"sess_cb_cache","num_turns":85,"usage":{"input_tokens":1555562,"cache_read_input_tokens":1428800,"cache_creation_input_tokens":2300,"output_tokens":4839}}`
	parseCodeBuddyJSONOutput(trace, stdout)

	if trace.InteractionCount != 85 {
		t.Fatalf("interaction_count = %d, want 85", trace.InteractionCount)
	}
	if trace.PromptTokens == nil || *trace.PromptTokens != 126762 {
		t.Fatalf("prompt_tokens = %v, want net 126762", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 4839 {
		t.Fatalf("completion_tokens = %v, want 4839", trace.CompletionTokens)
	}
	if trace.TotalTokens == nil || *trace.TotalTokens != 131601 {
		t.Fatalf("total_tokens = %v, want 131601", trace.TotalTokens)
	}
	if trace.RawInputTokens == nil || *trace.RawInputTokens != 1555562 {
		t.Fatalf("raw_input_tokens = %v, want 1555562", trace.RawInputTokens)
	}
	if trace.CacheReadTokens == nil || *trace.CacheReadTokens != 1428800 {
		t.Fatalf("cache_read_input_tokens = %v, want 1428800", trace.CacheReadTokens)
	}
	if trace.CacheCreateTokens == nil || *trace.CacheCreateTokens != 2300 {
		t.Fatalf("cache_creation_input_tokens = %v, want 2300", trace.CacheCreateTokens)
	}
}

func TestParseCodeBuddyJSONOutputWithMixedOutput(t *testing.T) {
	trace := &AgentTrace{Framework: "codebuddy"}
	stdout := "Some log lines\nTool call: write_file\nAnother log line\n" +
		`{"result":"Done","session_id":"sess_mixed","usage":{"prompt_tokens":3000,"completion_tokens":800,"total_tokens":3800}}`
	parseCodeBuddyJSONOutput(trace, stdout)

	if trace.SessionID != "sess_mixed" {
		t.Fatalf("session_id = %q, want %q", trace.SessionID, "sess_mixed")
	}
	if trace.PromptTokens == nil || *trace.PromptTokens != 3000 {
		t.Fatalf("prompt_tokens = %v, want 3000", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 800 {
		t.Fatalf("completion_tokens = %v, want 800", trace.CompletionTokens)
	}
}

func TestFinalizeAgentAccountingEstimatesMissingUsage(t *testing.T) {
	trace := &AgentTrace{Framework: "opencode"}
	model := modelConfig{
		Name: "agent-model",
		Pricing: modelPricing{
			PromptPer1KUSD:     0.001,
			CompletionPer1KUSD: 0.002,
		},
	}

	finalizeAgentAccounting(trace, "请生成测试\nfunc Add(a int, b int) int", "func TestAdd(t *testing.T) { assert.Equal(t, 3, Add(1, 2)) }", model)

	if trace.TokenSource != "estimated" {
		t.Fatalf("token source = %q, want estimated", trace.TokenSource)
	}
	if trace.UsageSourceDetail != "prompt_and_generated_test_heuristic" {
		t.Fatalf("usage source detail = %q", trace.UsageSourceDetail)
	}
	if trace.PromptTokens == nil || *trace.PromptTokens <= 0 {
		t.Fatalf("expected prompt token estimate, got %+v", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens <= 0 {
		t.Fatalf("expected completion token estimate, got %+v", trace.CompletionTokens)
	}
	if trace.TotalTokens == nil || *trace.TotalTokens != *trace.PromptTokens+*trace.CompletionTokens {
		t.Fatalf("unexpected total tokens: prompt=%v completion=%v total=%v", trace.PromptTokens, trace.CompletionTokens, trace.TotalTokens)
	}
	if trace.EstimatedCost == nil || *trace.EstimatedCost <= 0 {
		t.Fatalf("expected estimated cost, got %+v", trace.EstimatedCost)
	}
	if trace.CostSource != "estimated_tokens+configured_pricing" {
		t.Fatalf("cost source = %q", trace.CostSource)
	}
}

func TestFinalizeAgentAccountingMarksMixedUsagePartial(t *testing.T) {
	total := 1000
	trace := &AgentTrace{
		Framework:    "opencode",
		TotalTokens:  &total,
		TokenSource:  "actual",
		PromptTokens: nil,
	}

	finalizeAgentAccounting(trace, "prompt text", "generated code", modelConfig{})

	if trace.TokenSource != "partial" {
		t.Fatalf("token source = %q, want partial", trace.TokenSource)
	}
	if trace.PromptTokens == nil || trace.CompletionTokens == nil {
		t.Fatalf("expected missing token fields to be estimated: %+v", trace)
	}
	if trace.TotalTokens == nil || *trace.TotalTokens != 1000 {
		t.Fatalf("actual total tokens should be preserved, got %+v", trace.TotalTokens)
	}
}

func TestSummarizeAgentCommandErrorPrefersActionableTail(t *testing.T) {
	stderr := strings.Join([]string{
		"Reading additional input from stdin...",
		"OpenAI Codex v0.43.0",
		"2026-05-06T04:59:11.123Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket: HTTP error: 401 Unauthorized, url: wss://api.openai.com/v1/responses",
		"ERROR: Reconnecting... 5/5",
	}, "\n")

	got := summarizeAgentCommandError("", stderr, "exit status 1", 300)
	if strings.Contains(got, "Reading additional input") {
		t.Fatalf("summary kept startup prefix: %q", got)
	}
	if !strings.Contains(got, "401 Unauthorized") {
		t.Fatalf("summary = %q, want actionable 401 detail", got)
	}
}

func TestSummarizeAgentCommandErrorExtractsClaudeCodeJSONLAuthFailure(t *testing.T) {
	stdout := strings.Join([]string{
		`{"line":"{\"type\":\"system\",\"subtype\":\"api_retry\",\"error_status\":401,\"error\":\"authentication_failed\"}","line_no":1,"stream":"stdout"}`,
		`{"line":"{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":true,\"api_error_status\":401,\"result\":\"Failed to authenticate. API Error: 401 Invalid API Key\"}","line_no":2,"stream":"stdout"}`,
	}, "\n")

	got := summarizeAgentCommandError(stdout, "", "", 300)
	if !strings.Contains(got, "API error 401") {
		t.Fatalf("summary = %q, want API status", got)
	}
	if !strings.Contains(got, "Invalid API Key") {
		t.Fatalf("summary = %q, want invalid key detail", got)
	}
}

func TestExtractFinalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single JSON object",
			input: `{"key":"value"}`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "JSON at end of mixed output",
			input: "log line 1\nlog line 2\n{\"result\":\"ok\"}",
			want:  `{"result":"ok"}`,
		},
		{
			name:  "no JSON",
			input: "just plain text",
			want:  "",
		},
		{
			name:  "nested braces in JSON",
			input: `{"outer":{"inner":"val"}}`,
			want:  `{"outer":{"inner":"val"}}`,
		},
		{
			name:  "JSON array",
			input: `[{"type":"message","role":"user"},{"type":"message","role":"assistant"}]`,
			want:  `[{"type":"message","role":"user"},{"type":"message","role":"assistant"}]`,
		},
		{
			name:  "JSON array with trailing text",
			input: "some log\n[{\"type\":\"msg\"}]",
			want:  `[{"type":"msg"}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFinalJSON(tt.input)
			if got != tt.want {
				t.Fatalf("extractFinalJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseUsageAndSessionWithCodeBuddy(t *testing.T) {
	trace := &AgentTrace{Framework: "codebuddy"}
	stdout := `{"result":"Done","session_id":"sess_cb","usage":{"input_tokens":2000,"output_tokens":500}}`
	parseUsageAndSession(trace, stdout, "")

	if trace.SessionID != "sess_cb" {
		t.Fatalf("session_id = %q, want %q", trace.SessionID, "sess_cb")
	}
	if trace.PromptTokens == nil || *trace.PromptTokens != 2000 {
		t.Fatalf("prompt_tokens = %v, want 2000", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 500 {
		t.Fatalf("completion_tokens = %v, want 500", trace.CompletionTokens)
	}
	if got, want := trace.UsageSourceDetail, "codebuddy_json_output"; got != want {
		t.Fatalf("usage_source_detail = %q, want %q", got, want)
	}
}

func TestParseClaudeCodeJSONOutput(t *testing.T) {
	trace := &AgentTrace{Framework: "claudecode"}
	stdout := `{"session_id":"sess_claude","usage":{"input_tokens":4100,"output_tokens":900,"total_tokens":5000}}`
	parseClaudeCodeJSONOutput(trace, stdout)

	if trace.SessionID != "sess_claude" {
		t.Fatalf("session_id = %q, want %q", trace.SessionID, "sess_claude")
	}
	if trace.PromptTokens == nil || *trace.PromptTokens != 4100 {
		t.Fatalf("prompt_tokens = %v, want 4100", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 900 {
		t.Fatalf("completion_tokens = %v, want 900", trace.CompletionTokens)
	}
	if trace.TotalTokens == nil || *trace.TotalTokens != 5000 {
		t.Fatalf("total_tokens = %v, want 5000", trace.TotalTokens)
	}
	if got, want := trace.UsageSourceDetail, "claudecode_json_output"; got != want {
		t.Fatalf("usage_source_detail = %q, want %q", got, want)
	}
}

func TestParseClaudeCodeJSONOutputDoesNotDoubleCountModelUsage(t *testing.T) {
	trace := &AgentTrace{Framework: "claudecode"}
	stdout := `{"type":"result","session_id":"sess_claude","usage":{"input_tokens":14349,"output_tokens":5667},"modelUsage":{"mimo-v2.5":{"input_tokens":14349,"output_tokens":5667}}}`
	parseClaudeCodeJSONOutput(trace, stdout)

	if trace.PromptTokens == nil || *trace.PromptTokens != 14349 {
		t.Fatalf("prompt_tokens = %v, want 14349", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 5667 {
		t.Fatalf("completion_tokens = %v, want 5667", trace.CompletionTokens)
	}
	if trace.TotalTokens == nil || *trace.TotalTokens != 20016 {
		t.Fatalf("total_tokens = %v, want 20016 without modelUsage double count", trace.TotalTokens)
	}
}

func TestParseAgentOutputExtractsCommandsFromStructuredToolCalls(t *testing.T) {
	trace := &AgentTrace{Framework: "claudecode", Command: "claude -p prompt"}
	stdout := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"result","num_turns":7,"usage":{"input_tokens":1200,"output_tokens":300}}`,
	}, "\n")
	parseAgentOutput(trace, stdout, "")

	if trace.InteractionCount != 7 {
		t.Fatalf("interaction_count = %d, want 7", trace.InteractionCount)
	}
	if len(trace.CommandsExecuted) != 1 || trace.CommandsExecuted[0] != "go test ./..." {
		t.Fatalf("commands_executed = %+v, want go test ./...", trace.CommandsExecuted)
	}
	if len(trace.ToolCalls) != 1 || trace.ToolCalls[0].Tool != "Bash" {
		t.Fatalf("tool calls = %+v, want one Bash call", trace.ToolCalls)
	}
}

func TestParseUsageAndSessionWithClaudeCodeJSONL(t *testing.T) {
	trace := &AgentTrace{Framework: "claudecode"}
	stdout := strings.Join([]string{
		`{"type":"init","session_id":"sess_stream"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"result","usage":{"input_tokens":1200,"output_tokens":300,"total_tokens":1500}}`,
	}, "\n")
	parseUsageAndSession(trace, stdout, "")

	if trace.SessionID != "sess_stream" {
		t.Fatalf("session_id = %q, want %q", trace.SessionID, "sess_stream")
	}
	if trace.PromptTokens == nil || *trace.PromptTokens != 1200 {
		t.Fatalf("prompt_tokens = %v, want 1200", trace.PromptTokens)
	}
	if trace.CompletionTokens == nil || *trace.CompletionTokens != 300 {
		t.Fatalf("completion_tokens = %v, want 300", trace.CompletionTokens)
	}
	if got, want := trace.UsageSourceDetail, "claudecode_jsonl_lines"; got != want {
		t.Fatalf("usage_source_detail = %q, want %q", got, want)
	}
	tools := parseClaudeCodeToolCalls(stdout, "")
	if len(tools) != 1 || tools[0].Tool != "bash" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestBuildTrajectoryFromStreamJSON(t *testing.T) {
	stdout := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I will inspect the file."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/workspace/foo.py"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"def add(a,b): return a+b"}]}}`,
		`{"type":"result","usage":{"input_tokens":1200,"output_tokens":300,"total_tokens":1500}}`,
	}, "\n")
	trace := AgentTrace{
		SubjectID:      "claudecode__m__skill",
		Framework:      "claudecode",
		Model:          "m",
		Skill:          "skill",
		SampleID:       "sample",
		Language:       "python",
		RawTracePath:   "raw.jsonl",
		RawStdoutPath:  "stdout.log",
		RawStderrPath:  "stderr.log",
		TrajectoryPath: "trajectory.json",
	}
	traj := buildAgentTrajectory(trace, "generated_test.py", "", stdout, "")

	if traj.RawTracePath != "raw.jsonl" {
		t.Fatalf("raw trace path = %q", traj.RawTracePath)
	}
	if len(traj.Steps) < 4 {
		t.Fatalf("steps len = %d, want at least 4: %+v", len(traj.Steps), traj.Steps)
	}
	if traj.Steps[1].Kind != "tool_call" || traj.Steps[1].Tool != "Read" || traj.Steps[1].ToolCallID != "toolu_1" {
		t.Fatalf("tool call step unexpected: %+v", traj.Steps[1])
	}
	foundResult := false
	for _, step := range traj.Steps {
		if step.Kind == "tool_result" && step.ToolCallID == "toolu_1" {
			foundResult = true
			break
		}
	}
	if !foundResult {
		t.Fatalf("expected tool_result for toolu_1 in steps: %+v", traj.Steps)
	}
}

func TestBuildTrajectoryCompactsRawEventsAndParsesExitCode(t *testing.T) {
	longText := strings.Repeat("long trace content ", 800)
	stdout := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"I will inspect the source and generate tests. ` + longText + `"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"javac -cp . Source.java generated_test.java 2>&1","huge":"` + longText + `"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"javac: not found\nExit Code: 127","is_error":false}]}}`,
	}, "\n")
	trace := AgentTrace{
		SubjectID: "claudecode__m__skill",
		Framework: "claudecode",
		Model:     "m",
		Skill:     "skill",
		SampleID:  "sample",
		Language:  "java",
	}

	traj := buildAgentTrajectory(trace, "generated_test.java", "", stdout, "")
	raw, err := json.Marshal(traj)
	if err != nil {
		t.Fatalf("marshal trajectory: %v", err)
	}
	var decoded AgentTrajectory
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal trajectory: %v", err)
	}

	foundThinking := false
	foundFailedBash := false
	for _, step := range decoded.Steps {
		if step.Kind == "thinking" {
			foundThinking = true
			if len(step.Text) > 4500 {
				t.Fatalf("thinking text was not compacted: len=%d", len(step.Text))
			}
		}
		if step.Kind == "tool_result" && step.ToolCallID == "toolu_bash" {
			foundFailedBash = true
			if step.ExitCode == nil || *step.ExitCode != 127 {
				t.Fatalf("exit_code = %v, want 127", step.ExitCode)
			}
			if step.Success == nil || *step.Success {
				t.Fatalf("success = %v, want false for non-zero exit", step.Success)
			}
		}
		if step.RawEvent != nil {
			if b, err := json.Marshal(step.RawEvent); err == nil && len(b) > 20000 {
				t.Fatalf("raw_event too large: %d bytes", len(b))
			}
		}
	}
	if !foundThinking {
		t.Fatalf("expected thinking step in %+v", decoded.Steps)
	}
	if !foundFailedBash {
		t.Fatalf("expected failed bash tool_result in %+v", decoded.Steps)
	}
}

func TestWriteAgentTraceJSONLEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.trace.jsonl")
	trace := AgentTrace{
		SubjectID:        "claudecode__m__skill",
		Framework:        "claudecode",
		Model:            "m",
		Skill:            "skill",
		SampleID:         "sample",
		Language:         "java",
		InteractionCount: 3,
		ToolCalls: []ToolCall{
			{Tool: "Bash", Input: "go test ./...", Output: "ok", Success: true},
		},
		CommandsExecuted: []string{"go test ./..."},
		FilesRead:        []string{"src/main.go"},
		FilesWritten:     []string{"src/main_test.go"},
		RawTracePath:     "raw.jsonl",
		TrajectoryPath:   "trajectory.json",
		ExitCode:         0,
		Stdout:           strings.Repeat("stdout ", 900),
	}
	for i := 0; i < 260; i++ {
		trace.WorkspaceDiff = append(trace.WorkspaceDiff, "vendor/generated/path/file.go")
	}
	if err := writeAgentTrace(path, trace); err != nil {
		t.Fatalf("write agent trace: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read agent trace: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 5 {
		t.Fatalf("trace lines = %d, want at least 5: %s", len(lines), string(raw))
	}
	events := make([]string, 0, len(lines))
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid jsonl line %q: %v", line, err)
		}
		events = append(events, event["event"].(string))
		if event["event"] == "outcome" {
			if stdout, _ := event["stdout_excerpt"].(string); len(stdout) > 4500 {
				t.Fatalf("stdout excerpt too large: %d", len(stdout))
			}
			if diff, ok := event["workspace_diff"].([]any); !ok || len(diff) > 201 {
				t.Fatalf("workspace diff was not compacted: %T len=%d", event["workspace_diff"], len(diff))
			}
			if count, _ := event["workspace_diff_count"].(float64); int(count) != len(trace.WorkspaceDiff) {
				t.Fatalf("workspace_diff_count = %v, want %d", event["workspace_diff_count"], len(trace.WorkspaceDiff))
			}
		}
	}
	for _, want := range []string{"summary", "tool_call", "command", "file_read", "file_written", "outcome"} {
		found := false
		for _, got := range events {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing event %q in %v", want, events)
		}
	}
}

func TestBuildTrajectorySupplementsParsedToolCalls(t *testing.T) {
	trace := AgentTrace{
		SubjectID: "opencode__m__no_skill",
		Framework: "opencode",
		Model:     "m",
		Skill:     "no_skill",
		SampleID:  "sample",
		Language:  "go",
		ToolCalls: []ToolCall{
			{Tool: "bash", Input: "go test ./...", Output: "ok", Success: true},
		},
	}
	stdout := "INFO run completed"

	traj := buildAgentTrajectory(trace, "generated_test.go", "", stdout, "")
	found := false
	for _, step := range traj.Steps {
		if step.Kind == "tool_call" && step.Source == "parsed_log" && step.Tool == "bash" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected parsed tool call supplement in %+v", traj.Steps)
	}
}

func TestDiffSnapshotsFiltersAgentRuntimeNoise(t *testing.T) {
	before := map[string]string{
		"boundary_000.java": "source",
	}
	after := map[string]string{
		"boundary_000.java":              "source",
		"generated_test.java":            "test",
		".claude/projects/session.jsonl": "claude",
		".codebuddy/plugins/marketplaces/codebuddy-plugins-official/README.md": "plugin",
		".codebuddy/local_storage/entry.info":                                  "storage",
		".pytest_cache/v/cache/nodeids":                                        "pytest",
		".utbench/xdg-config/opencode/node_modules/pkg/index.js":               "node",
		".utbench/xdg-data/opencode/opencode.db":                               "db",
		"go.mod":                                                               "module",
		"test_runner":                                                          "runner",
	}

	got := diffSnapshots(before, after)
	if len(got) != 1 || got[0] != "generated_test.java" {
		t.Fatalf("diffSnapshots = %#v, want only generated_test.java", got)
	}
}

func TestParseOpenCodeSessionTrajectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_export.json")
	raw := `{
	  "info": {"id": "ses_test"},
	  "messages": [
	    {"role":"assistant","content":[{"type":"text","text":"Reading source"},{"type":"tool_use","id":"call_1","name":"read","input":{"path":"foo.py"}}]},
	    {"role":"tool","content":[{"type":"tool_result","tool_use_id":"call_1","content":"source"}]}
	  ]
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write session export: %v", err)
	}

	steps := parseOpenCodeSessionTrajectory(path)
	if len(steps) < 3 {
		t.Fatalf("steps len = %d, want at least 3: %+v", len(steps), steps)
	}
	if steps[1].Kind != "tool_call" || steps[1].Tool != "read" {
		t.Fatalf("tool call step unexpected: %+v", steps[1])
	}
	if steps[2].Kind != "tool_result" || steps[2].ToolCallID != "call_1" {
		t.Fatalf("tool result step unexpected: %+v", steps[2])
	}
}

func TestParseOpenCodeSessionTrajectoryPartsSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_export.json")
	raw := `{
	  "info": {"id": "ses_test"},
	  "messages": [
	    {
	      "info": {"role": "assistant", "id": "msg_1"},
	      "parts": [
	        {"type": "step-start"},
	        {"type": "reasoning", "text": "Need to write and verify the generated test."},
	        {"type": "tool", "tool": "write", "callID": "call_write", "state": {
	          "status": "completed",
	          "input": {"filePath": "/workspace/generated_test.java", "content": "class T {}"},
	          "output": "Wrote file successfully.",
	          "time": {"start": 1000, "end": 1025}
	        }},
	        {"type": "text", "text": "Done"}
	      ]
	    },
	    {
	      "info": {"role": "assistant", "id": "msg_2"},
	      "parts": [
	        {"type": "tool", "tool": "read", "callID": "call_read", "state": {
	          "status": "completed",
	          "input": {"filePath": "/workspace/generated_test.java"},
	          "output": "class T {}"
	        }}
	      ]
	    }
	  ]
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write session export: %v", err)
	}

	steps := parseOpenCodeSessionTrajectory(path)
	if len(steps) != 6 {
		t.Fatalf("steps len = %d, want 6: %+v", len(steps), steps)
	}
	if steps[0].Kind != "thinking" || !strings.Contains(steps[0].Text, "write and verify") {
		t.Fatalf("thinking step unexpected: %+v", steps[0])
	}
	if steps[1].Kind != "tool_call" || steps[1].Tool != "write" || steps[1].ToolCallID != "call_write" {
		t.Fatalf("write call step unexpected: %+v", steps[1])
	}
	if steps[2].Kind != "tool_result" || steps[2].Tool != "write" || steps[2].Success == nil || !*steps[2].Success || steps[2].DurationMS != 25 {
		t.Fatalf("write result step unexpected: %+v", steps[2])
	}
	if steps[3].Kind != "message" || steps[4].Tool != "read" || steps[5].Kind != "tool_result" {
		t.Fatalf("tail steps unexpected: %+v", steps)
	}
}

func TestExtractOpenCodeToolCalls(t *testing.T) {
	payload := map[string]any{
		"info": map[string]any{"id": "ses_test"},
		"messages": []any{
			map[string]any{
				"info": map[string]any{"role": "assistant", "id": "msg_1"},
				"parts": []any{
					map[string]any{"type": "step-start"},
					map[string]any{"type": "reasoning", "text": "Need to write test."},
					map[string]any{
						"type":   "tool",
						"tool":   "write",
						"callID": "call_write",
						"state": map[string]any{
							"status": "completed",
							"input":  map[string]any{"filePath": "/workspace/generated_test.go", "content": "package main"},
							"output": "Wrote file successfully.",
						},
					},
					map[string]any{
						"type":   "tool",
						"tool":   "bash",
						"callID": "call_bash",
						"state": map[string]any{
							"status": "completed",
							"input":  map[string]any{"command": "go test ./..."},
							"output": "ok",
						},
					},
				},
			},
			map[string]any{
				"info": map[string]any{"role": "assistant", "id": "msg_2"},
				"parts": []any{
					map[string]any{
						"type":   "tool",
						"tool":   "read",
						"callID": "call_read",
						"state": map[string]any{
							"status": "completed",
							"input":  map[string]any{"filePath": "/workspace/generated_test.go"},
							"output": "package main",
						},
					},
				},
			},
		},
	}

	calls := extractOpenCodeToolCalls(payload)
	if len(calls) != 3 {
		t.Fatalf("tool calls len = %d, want 3: %+v", len(calls), calls)
	}

	// 验证工具名称
	expectedTools := []string{"write", "bash", "read"}
	for i, call := range calls {
		if call.Tool != expectedTools[i] {
			t.Fatalf("tool[%d] = %q, want %q", i, call.Tool, expectedTools[i])
		}
		if !call.Success {
			t.Fatalf("tool[%d].Success = false, want true", i)
		}
	}
}

func TestExtractOpenCodeToolCallsIgnoresNonToolParts(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{
				"parts": []any{
					map[string]any{"type": "step-start"},
					map[string]any{"type": "reasoning", "text": "Thinking..."},
					map[string]any{"type": "text", "text": "Done"},
					map[string]any{
						"type":   "tool",
						"tool":   "bash",
						"callID": "call_1",
						"state": map[string]any{
							"status": "completed",
							"input":  map[string]any{"command": "ls"},
							"output": "file.go",
						},
					},
				},
			},
		},
	}

	calls := extractOpenCodeToolCalls(payload)
	if len(calls) != 1 {
		t.Fatalf("tool calls len = %d, want 1: %+v", len(calls), calls)
	}
	if calls[0].Tool != "bash" {
		t.Fatalf("tool = %q, want bash", calls[0].Tool)
	}
}

func TestUsageRecordFromMapSubtractsCacheReadTokens(t *testing.T) {
	// 模拟 CodeBuddy 的 token 数据，其中 input_tokens 包含缓存读取的 token
	v := map[string]any{
		"input_tokens":            float64(1125817),
		"output_tokens":           float64(6496),
		"cache_read_input_tokens": float64(1104896),
	}

	record, ok := usageRecordFromMap(v)
	if !ok {
		t.Fatal("expected usage record to be extracted")
	}

	// input_tokens - cache_read_input_tokens = 1125817 - 1104896 = 20921
	if record.Prompt == nil || *record.Prompt != 20921 {
		t.Fatalf("prompt tokens = %v, want 20921 (input_tokens - cache_read_input_tokens)", record.Prompt)
	}
	if record.Completion == nil || *record.Completion != 6496 {
		t.Fatalf("completion tokens = %v, want 6496", record.Completion)
	}
	// total = prompt + completion = 20921 + 6496 = 27417
	if record.Total == nil || *record.Total != 27417 {
		t.Fatalf("total tokens = %v, want 27417", record.Total)
	}
}

func TestUsageRecordFromMapWithoutCacheTokens(t *testing.T) {
	// 没有缓存token的情况
	v := map[string]any{
		"input_tokens":  float64(1000),
		"output_tokens": float64(200),
	}

	record, ok := usageRecordFromMap(v)
	if !ok {
		t.Fatal("expected usage record to be extracted")
	}

	if record.Prompt == nil || *record.Prompt != 1000 {
		t.Fatalf("prompt tokens = %v, want 1000", record.Prompt)
	}
	if record.Completion == nil || *record.Completion != 200 {
		t.Fatalf("completion tokens = %v, want 200", record.Completion)
	}
	if record.Total == nil || *record.Total != 1200 {
		t.Fatalf("total tokens = %v, want 1200", record.Total)
	}
}
