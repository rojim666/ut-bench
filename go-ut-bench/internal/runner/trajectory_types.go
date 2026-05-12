package runner

import "time"

// AgentTrajectory 是面向分析和回放的统一 step-by-step 执行轨迹。
type AgentTrajectory struct {
	SchemaVersion     string            `json:"schema_version"`
	SubjectID         string            `json:"subject_id"`
	Framework         string            `json:"framework"`
	Model             string            `json:"model"`
	Skill             string            `json:"skill"`
	SampleID          string            `json:"sample_id"`
	Language          string            `json:"language"`
	SessionID         string            `json:"session_id,omitempty"`
	StartedAt         time.Time         `json:"started_at"`
	FinishedAt        time.Time         `json:"finished_at"`
	RawTracePath      string            `json:"raw_trace_path,omitempty"`
	RawStdoutPath     string            `json:"raw_stdout_path,omitempty"`
	RawStderrPath     string            `json:"raw_stderr_path,omitempty"`
	SessionExportPath string            `json:"session_export_path,omitempty"`
	Steps             []TrajectoryStep  `json:"steps"`
	Outcome           TrajectoryOutcome `json:"outcome"`
}

type TrajectoryStep struct {
	Index      int            `json:"index"`
	Kind       string         `json:"kind"`
	Role       string         `json:"role,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Input      any            `json:"input,omitempty"`
	Output     any            `json:"output,omitempty"`
	Text       string         `json:"text,omitempty"`
	Success    *bool          `json:"success,omitempty"`
	ExitCode   *int           `json:"exit_code,omitempty"`
	DurationMS int            `json:"duration_ms,omitempty"`
	Source     string         `json:"source,omitempty"`
	RawType    string         `json:"raw_type,omitempty"`
	RawEvent   map[string]any `json:"raw_event,omitempty"`
}

type TrajectoryOutcome struct {
	ExitCode          int      `json:"exit_code"`
	DurationMS        int      `json:"duration_ms"`
	GeneratedTestPath string   `json:"generated_test_path,omitempty"`
	WorkspaceDiff     []string `json:"workspace_diff,omitempty"`
	Error             string   `json:"error,omitempty"`
}
