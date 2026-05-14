package contracts

import "time"

const AnalysisSchemaVersion = "analysis.v0.1.1"
const OptimizationPlanSchemaVersion = "optimization_plan.v0.1.0"

// AnalysisReport 是一次 run 的规则诊断与 LLM 诊断报告。
type AnalysisReport struct {
	SchemaVersion   string                   `json:"schema_version"`
	RunID           string                   `json:"run_id"`
	GeneratedAt     time.Time                `json:"generated_at"`
	SourceFiles     AnalysisSourceFiles      `json:"source_files"`
	LLMStatus       LLMAnalysisStatus        `json:"llm_status"`
	Summary         AnalysisSummary          `json:"summary"`
	TraceQuality    AnalysisTraceQuality     `json:"trace_quality"`
	Subjects        []AnalysisSubject        `json:"subjects"`
	Findings        []AnalysisFinding        `json:"findings"`
	Recommendations []AnalysisRecommendation `json:"recommendations"`
	LLM             *LLMAnalysisResult       `json:"llm,omitempty"`
}

type AnalysisSourceFiles struct {
	ManifestPath   string `json:"manifest_path,omitempty"`
	EvaluationPath string `json:"evaluation_path,omitempty"`
	ReportPath     string `json:"report_path,omitempty"`
}

type AnalysisSummary struct {
	SubjectCount        int      `json:"subject_count"`
	ResultCount         int      `json:"result_count"`
	FindingCount        int      `json:"finding_count"`
	CriticalCount       int      `json:"critical_count"`
	WarningCount        int      `json:"warning_count"`
	RecommendationCount int      `json:"recommendation_count"`
	Headline            string   `json:"headline"`
	KeyPoints           []string `json:"key_points,omitempty"`
}

type AnalysisTraceQuality struct {
	TotalSubjects          int `json:"total_subjects"`
	SubjectsWithTrajectory int `json:"subjects_with_trajectory"`
	SubjectsWithRawTrace   int `json:"subjects_with_raw_trace"`
	TotalSteps             int `json:"total_steps"`
	ToolCallSteps          int `json:"tool_call_steps"`
	MessageSteps           int `json:"message_steps"`
	ThinkingSteps          int `json:"thinking_steps"`
}

type LLMAnalysisStatus struct {
	Enabled              bool   `json:"enabled"`
	Status               string `json:"status"` // disabled / skipped / ok / degraded / failed
	Model                string `json:"model,omitempty"`
	Message              string `json:"message,omitempty"`
	EvidenceCount        int    `json:"evidence_count,omitempty"`
	EvidenceSubjectCount int    `json:"evidence_subject_count,omitempty"`
}

type AnalysisSubject struct {
	SubjectID          string                   `json:"subject_id"`
	Model              string                   `json:"model"`
	AgentFramework     string                   `json:"agent_framework,omitempty"`
	AgentModel         string                   `json:"agent_model,omitempty"`
	SkillName          string                   `json:"skill_name,omitempty"`
	Language           string                   `json:"language"`
	SampleID           string                   `json:"sample_id"`
	CompilePass        bool                     `json:"compile_pass"`
	TestPass           *bool                    `json:"test_pass"`
	LineCoverage       *float64                 `json:"line_coverage,omitempty"`
	BranchCoverage     *float64                 `json:"branch_coverage,omitempty"`
	MutationScore      *float64                 `json:"mutation_score,omitempty"`
	LatencyMS          *int                     `json:"latency_ms,omitempty"`
	TotalTokens        *int                     `json:"total_tokens,omitempty"`
	TrajectoryPath     string                   `json:"trajectory_path,omitempty"`
	RawTracePath       string                   `json:"raw_trace_path,omitempty"`
	WorkspaceDiffPath  string                   `json:"workspace_diff_path,omitempty"`
	GeneratedTestPath  string                   `json:"generated_test_path,omitempty"`
	SourcePath         string                   `json:"source_path,omitempty"`
	TraceStepCount     int                      `json:"trace_step_count"`
	ToolCallCount      int                      `json:"tool_call_count"`
	HasSourceRead      bool                     `json:"has_source_read"`
	HasTestWrite       bool                     `json:"has_test_write"`
	HasTestExecution   bool                     `json:"has_test_execution"`
	ModifiedSource     bool                     `json:"modified_source"`
	RuntimeNoiseCount  int                      `json:"runtime_noise_count"`
	PolicyCommandCount int                      `json:"policy_command_count"`
	Trajectory         []AnalysisTrajectoryStep `json:"trajectory,omitempty"`
}

type AnalysisTrajectoryStep struct {
	Index         int    `json:"index"`
	Kind          string `json:"kind"`
	Role          string `json:"role,omitempty"`
	Tool          string `json:"tool,omitempty"`
	Success       *bool  `json:"success,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	DurationMS    int    `json:"duration_ms,omitempty"`
	Source        string `json:"source,omitempty"`
	TextExcerpt   string `json:"text_excerpt,omitempty"`
	InputExcerpt  string `json:"input_excerpt,omitempty"`
	OutputExcerpt string `json:"output_excerpt,omitempty"`
}

type AnalysisFinding struct {
	ID             string        `json:"id"`
	Severity       string        `json:"severity"`
	Category       string        `json:"category"`
	SubjectID      string        `json:"subject_id,omitempty"`
	SampleID       string        `json:"sample_id,omitempty"`
	Title          string        `json:"title"`
	Detail         string        `json:"detail"`
	Recommendation string        `json:"recommendation,omitempty"`
	Confidence     float64       `json:"confidence,omitempty"`
	Source         string        `json:"source"` // rule / llm
	Evidence       []EvidenceRef `json:"evidence,omitempty"`
}

type EvidenceRef struct {
	EvidenceID string `json:"evidence_id,omitempty"`
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	SubjectID  string `json:"subject_id,omitempty"`
	SampleID   string `json:"sample_id,omitempty"`
	StepIndex  int    `json:"step_index,omitempty"`
	Excerpt    string `json:"excerpt,omitempty"`
}

type AnalysisRecommendation struct {
	ID             string        `json:"id"`
	Priority       string        `json:"priority"`
	Category       string        `json:"category"`
	Target         string        `json:"target,omitempty"`
	Title          string        `json:"title"`
	Detail         string        `json:"detail"`
	ExpectedImpact string        `json:"expected_impact,omitempty"`
	Risk           string        `json:"risk,omitempty"`
	AppliesTo      []string      `json:"applies_to,omitempty"`
	Source         string        `json:"source"`
	Evidence       []EvidenceRef `json:"evidence,omitempty"`
}

type LLMAnalysisResult struct {
	Model           string                   `json:"model"`
	PromptVersion   string                   `json:"prompt_version"`
	Status          string                   `json:"status"`
	Summary         string                   `json:"summary,omitempty"`
	RawOutput       string                   `json:"raw_output,omitempty"`
	Findings        []AnalysisFinding        `json:"findings,omitempty"`
	Recommendations []AnalysisRecommendation `json:"recommendations,omitempty"`
	Error           string                   `json:"error,omitempty"`
}

// OptimizationPlan 是从 AI 分析报告生成的可人工执行优化方案。
type OptimizationPlan struct {
	SchemaVersion  string              `json:"schema_version"`
	RunID          string              `json:"run_id"`
	GeneratedAt    time.Time           `json:"generated_at"`
	SourceAnalysis string              `json:"source_analysis"`
	LLMStatus      LLMAnalysisStatus   `json:"llm_status"`
	Summary        OptimizationSummary `json:"summary"`
	Items          []OptimizationItem  `json:"items"`
	RawLLMOutput   string              `json:"raw_llm_output,omitempty"`
	LLMError       string              `json:"llm_error,omitempty"`
}

type OptimizationSummary struct {
	ItemCount         int      `json:"item_count"`
	HighPriorityCount int      `json:"high_priority_count"`
	LLMItemCount      int      `json:"llm_item_count"`
	RuleItemCount     int      `json:"rule_item_count"`
	Targets           []string `json:"targets,omitempty"`
	Headline          string   `json:"headline"`
	KeyPoints         []string `json:"key_points,omitempty"`
}

type OptimizationItem struct {
	ID              string                     `json:"id"`
	Priority        string                     `json:"priority"`
	Target          string                     `json:"target"`
	Category        string                     `json:"category"`
	Title           string                     `json:"title"`
	Detail          string                     `json:"detail"`
	AppliesTo       []string                   `json:"applies_to,omitempty"`
	Source          string                     `json:"source"` // rule / llm
	SourceRefs      []string                   `json:"source_refs,omitempty"`
	ExpectedMetrics []string                   `json:"expected_metrics,omitempty"`
	Actions         []OptimizationAction       `json:"actions"`
	Risks           []OptimizationRisk         `json:"risks,omitempty"`
	Verification    []OptimizationVerification `json:"verification"`
	Evidence        []OptimizationEvidenceRef  `json:"evidence,omitempty"`
	Confidence      float64                    `json:"confidence,omitempty"`
}

type OptimizationAction struct {
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	FileHint string `json:"file_hint,omitempty"`
}

type OptimizationVerification struct {
	Command    string `json:"command,omitempty"`
	ManualStep string `json:"manual_step,omitempty"`
	Expected   string `json:"expected"`
}

type OptimizationRisk struct {
	Description string `json:"description"`
	Mitigation  string `json:"mitigation,omitempty"`
	Rollback    string `json:"rollback,omitempty"`
}

type OptimizationEvidenceRef struct {
	EvidenceID string `json:"evidence_id,omitempty"`
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	SubjectID  string `json:"subject_id,omitempty"`
	SampleID   string `json:"sample_id,omitempty"`
	StepIndex  int    `json:"step_index,omitempty"`
	Excerpt    string `json:"excerpt,omitempty"`
	Source     string `json:"source,omitempty"`
}
