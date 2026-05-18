package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go-ut-bench/internal/contracts"
)

// generateCLIAgent 在沙箱中执行 CLI Agent 并收集丰富化的 trace。
func generateCLIAgent(ctx context.Context, sandboxRunner SandboxRunner, req AgentGenerateRequest) AgentGenerateResult {
	subjectID := req.Subject.Spec.ID
	framework := req.Subject.Framework
	skill := req.Subject.Skill
	sample := req.Sample
	strategy := resolveGenerationStrategy(sample)

	// 1. 准备独立 workspace
	workRoot := filepath.Join(req.OutputRoot, "runs", req.RunID, "agent_workspaces", subjectID, sample.Language, sample.ID)
	if err := os.RemoveAll(workRoot); err != nil {
		return agentError("workspace_error", err)
	}
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return agentError("workspace_error", err)
	}

	// 2. 复制样本到 workspace
	sourceFile, err := prepareAgentWorkspace(workRoot, sample)
	if err != nil {
		return agentError("workspace_error", err)
	}

	// 3. 注入 skill 文件
	outputFile := filepath.Join(workRoot, "generated_test"+languageExt(sample.Language))
	var skillDir string
	if strings.EqualFold(defaultString(skill.InjectMode, "prompt_append"), "agent_native") {
		// agent_native: 写入 Agent 框架原生 skill 目录（CodeBuddy: .codebuddy/skills/, OpenCode: .opencode/skills/）
		nativeDir, nativeErr := injectAgentNativeSkill(workRoot, framework.Name, skill)
		if nativeErr != nil {
			return agentError("skill_injection_error", nativeErr)
		}
		skillDir = nativeDir
	} else {
		// prompt_append / workspace_mount: 复制到 .utbench/skills/
		utbenchDir, err := injectSkillWorkspace(workRoot, skill)
		if err != nil {
			return agentError("skill_injection_error", err)
		}
		skillDir = utbenchDir
	}

	// 4. 构建容器内路径提示
	// 注意：如果 sandbox_mode=docker 但当前环境没有 Docker daemon，会降级为 local。
	effectiveSandboxMode := framework.SandboxMode
	if strings.EqualFold(effectiveSandboxMode, "docker") && !isDockerAvailable() {
		effectiveSandboxMode = "local"
	}
	outputHint := outputFile
	sourceHint := sourceFile
	skillHint := skillDir
	if !strings.EqualFold(effectiveSandboxMode, "local") {
		outputHint = "/workspace/generated_test" + languageExt(sample.Language)
		sourceHint = "/workspace/" + filepath.ToSlash(mustRel(workRoot, sourceFile))
		if skillDir != "" {
			skillHint = "/workspace/" + filepath.ToSlash(mustRel(workRoot, skillDir))
		}
	}

	// 5. 写入 Agent 任务说明
	agentPrompt := buildAgentPrompt(req.Prompt, sample, sourceHint, outputHint, skillHint, req.Subject.Spec.Framework, skill.Name, strategy)
	promptFile := filepath.Join(workRoot, "utbench_agent_prompt.md")
	if err := os.WriteFile(promptFile, []byte(agentPrompt), 0o644); err != nil {
		return agentError("workspace_error", err)
	}

	// 6. 执行前快照
	before, _ := snapshotWorkspace(workRoot)

	// 7. 准备 trace 输出目录
	traceDir := filepath.Join(req.MetaRoot, "agent_traces", subjectID, sample.Language)
	tracePath := filepath.Join(traceDir, sample.ID+".trace.jsonl")
	rawTracePath := filepath.Join(traceDir, sample.ID+".raw.jsonl")
	rawStdoutPath := filepath.Join(traceDir, sample.ID+".stdout.txt")
	rawStderrPath := filepath.Join(traceDir, sample.ID+".stderr.txt")
	trajectoryPath := filepath.Join(traceDir, sample.ID+".trajectory.json")
	diffPath := filepath.Join(traceDir, sample.ID+".diff.json")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		return agentError("trace_error", err)
	}

	// 8. 渲染命令和环境变量
	templateData := commandTemplateData{
		Workspace:         workRoot,
		PromptFile:        promptFile,
		OutputFile:        outputFile,
		SkillDir:          skillDir,
		Model:             req.Model.Name,
		ModelID:           req.Model.Model,
		ModelProvider:     req.Model.Provider,
		ModelEndpoint:     req.Model.Endpoint,
		AnthropicEndpoint: req.Model.AnthropicEndpoint,
		ModelAPIKeyEnv:    req.Model.APIKeyEnv,
		Framework:         req.Subject.Spec.Framework,
		SubjectID:         subjectID,
		Skill:             req.Subject.Spec.Skill,
		Language:          sample.Language,
		SampleID:          sample.ID,
		SourceFile:        sourceFile,
		ContainerWorkdir:  containerPath("/workspace", workRoot, effectiveSandboxMode),
		ContainerPrompt:   containerPath("/workspace/utbench_agent_prompt.md", promptFile, effectiveSandboxMode),
		ContainerOutput:   containerPath("/workspace/generated_test"+languageExt(sample.Language), outputFile, effectiveSandboxMode),
		ContainerSkillDir: containerPath("/workspace/.utbench/skills/"+safePathName(skill.Name), skillDir, effectiveSandboxMode),
	}
	cmdText, err := renderTemplateText("agent-command", framework.Command, templateData)
	if err != nil {
		return agentError("command_template_error", err)
	}
	if strings.TrimSpace(cmdText) == "" {
		return agentError("command_template_error", fmt.Errorf("cli agent command is empty"))
	}
	envMap, envFromHost, err := buildAgentEnv(framework, req.Model, templateData)
	if err != nil {
		return agentError("agent_env_error", err)
	}
	// 注入 run ID 以便 sandbox 容器能被 label 标记和清理
	envMap["UTBENCH_RUN_ID"] = req.RunID

	// 9. 平台托管的样本依赖准备
	sandboxReq := buildSandboxRunRequest(req.OutputRoot, framework, sample.Language, workRoot, cmdText, envMap, envFromHost)
	// 注意：源文件已经被 prepareAgentWorkspace 拷贝进 workspace 并 chmod 0444（只读），
	// 不再额外通过 `-v sourceFile:/workspace/readonly_sources/<basename>:ro` 单文件挂载。
	// 在 Docker Desktop (Windows/WSL2) 上单文件 bind-mount 与 workspace 主挂载并发会触发 9P
	// 同步竞态：host 上的源文件会被自动替换成空目录，prompt 文件也可能丢失，导致
	// `cat: /workspace/utbench_agent_prompt.md: No such file or directory` 与
	// `failed to fulfil mount request` 等假失败。
	environmentSetup, setupErr := runSandboxPreflight(ctx, sandboxRunner, sandboxReq, buildSampleEnvironmentSetupCommands(sample, workRoot))
	setupWarning := ""
	if setupErr != nil {
		trace := AgentTrace{
			SubjectID:          subjectID,
			Framework:          req.Subject.Spec.Framework,
			Model:              req.Subject.Spec.Model,
			Skill:              req.Subject.Spec.Skill,
			SampleID:           sample.ID,
			Language:           sample.Language,
			Command:            cmdText,
			StartedAt:          time.Now().UTC(),
			FinishedAt:         time.Now().UTC(),
			EnvironmentSetup:   environmentSetup,
			SandboxProvider:    sandboxReq.Provider,
			SandboxImage:       sandboxReq.DockerImage,
			SandboxFingerprint: sandboxFingerprintForRequest(sandboxReq),
			RawTracePath:       rawTracePath,
			RawStdoutPath:      rawStdoutPath,
			RawStderrPath:      rawStderrPath,
			TrajectoryPath:     trajectoryPath,
			TracePath:          tracePath,
			WorkspaceDiffPath:  diffPath,
		}
		_ = writeAgentTrace(tracePath, trace)
		return AgentGenerateResult{
			RawResponse: map[string]any{
				"adapter":             "cli_agent",
				"subject_id":          subjectID,
				"framework":           req.Subject.Spec.Framework,
				"model":               req.Subject.Spec.Model,
				"skill":               req.Subject.Spec.Skill,
				"command":             cmdText,
				"environment_setup":   environmentSetup,
				"sandbox_provider":    sandboxReq.Provider,
				"sandbox_image":       sandboxReq.DockerImage,
				"sandbox_mode":        sandboxReq.Mode,
				"sandbox_workspace":   workRoot,
				"sandbox_fingerprint": trace.SandboxFingerprint,
			},
			Trace: trace,
			Error: &contracts.ErrorInfo{
				Kind:      "sample_env_prepare_error",
				Message:   setupErr.Error(),
				Retryable: false,
			},
		}
	}

	// 10. 执行沙箱预检
	preflightChecks, preflightErr := runSandboxPreflight(ctx, sandboxRunner, sandboxReq, frameworkPreflightCommands(framework, sample.Language))
	if preflightErr != nil {
		trace := AgentTrace{
			SubjectID:          subjectID,
			Framework:          req.Subject.Spec.Framework,
			Model:              req.Subject.Spec.Model,
			Skill:              req.Subject.Spec.Skill,
			SampleID:           sample.ID,
			Language:           sample.Language,
			Command:            cmdText,
			StartedAt:          time.Now().UTC(),
			FinishedAt:         time.Now().UTC(),
			EnvironmentSetup:   environmentSetup,
			PreflightChecks:    preflightChecks,
			SandboxProvider:    sandboxReq.Provider,
			SandboxImage:       sandboxReq.DockerImage,
			SandboxFingerprint: sandboxFingerprintForRequest(sandboxReq),
			RawTracePath:       rawTracePath,
			RawStdoutPath:      rawStdoutPath,
			RawStderrPath:      rawStderrPath,
			TrajectoryPath:     trajectoryPath,
			TracePath:          tracePath,
			WorkspaceDiffPath:  diffPath,
		}
		return AgentGenerateResult{
			RawResponse: map[string]any{
				"adapter":             "cli_agent",
				"subject_id":          subjectID,
				"framework":           req.Subject.Spec.Framework,
				"model":               req.Subject.Spec.Model,
				"skill":               req.Subject.Spec.Skill,
				"command":             cmdText,
				"environment_setup":   environmentSetup,
				"preflight_checks":    preflightChecks,
				"sandbox_provider":    sandboxReq.Provider,
				"sandbox_image":       sandboxReq.DockerImage,
				"sandbox_mode":        sandboxReq.Mode,
				"sandbox_workspace":   workRoot,
				"sandbox_fingerprint": trace.SandboxFingerprint,
			},
			Trace: trace,
			Error: &contracts.ErrorInfo{
				Kind:      "sandbox_preflight_error",
				Message:   preflightErr.Error(),
				Retryable: false,
			},
		}
	}

	// 11. 执行主命令
	started := time.Now()
	runOutput, runErr := sandboxRunner.Run(ctx, sandboxReq)
	finished := time.Now()
	latency := int(finished.Sub(started).Milliseconds())
	_ = writeRawAgentOutputs(rawTracePath, rawStdoutPath, rawStderrPath, runOutput.Stdout, runOutput.Stderr)

	// 12. 执行后快照 + diff
	after, _ := snapshotWorkspace(workRoot)
	changes := diffSnapshots(before, after)

	// 13. 解析 Agent 输出，提取丰富化 trace 信息
	trace := AgentTrace{
		SubjectID:          subjectID,
		Framework:          req.Subject.Spec.Framework,
		Model:              req.Subject.Spec.Model,
		Skill:              req.Subject.Spec.Skill,
		SampleID:           sample.ID,
		Language:           sample.Language,
		Command:            cmdText,
		ExitCode:           runOutput.ExitCode,
		DurationMS:         latency,
		StartedAt:          started.UTC(),
		FinishedAt:         finished.UTC(),
		Stdout:             trimText(runOutput.Stdout, 8000),
		Stderr:             trimText(runOutput.Stderr, 8000),
		WorkspaceDiff:      changes,
		EnvironmentSetup:   environmentSetup,
		PreflightChecks:    preflightChecks,
		SandboxProvider:    sandboxReq.Provider,
		SandboxImage:       sandboxReq.DockerImage,
		SandboxFingerprint: sandboxFingerprintForRequest(sandboxReq),
		RawTracePath:       rawTracePath,
		RawStdoutPath:      rawStdoutPath,
		RawStderrPath:      rawStderrPath,
		TrajectoryPath:     trajectoryPath,
		TracePath:          tracePath,
		WorkspaceDiffPath:  diffPath,
	}

	// 从 Agent 输出中提取必要的 token、错误和策略校验信息，不再持久化完整 trace。
	parseAgentOutput(&trace, runOutput.Stdout, runOutput.Stderr)
	finalizeAgentAccounting(&trace, req.Prompt, "", req.Model)

	// 14. 写入 trace 文件（完整结构化数据）
	_ = writeAgentTrace(tracePath, trace)
	_ = contracts.WriteJSON(diffPath, map[string]any{
		"workspace":  workRoot,
		"subject_id": subjectID,
		"changes":    changes,
	})

	// 15. 构建原始响应
	rawResponse := map[string]any{
		"adapter":             "cli_agent",
		"subject_id":          subjectID,
		"framework":           req.Subject.Spec.Framework,
		"model":               req.Subject.Spec.Model,
		"skill":               req.Subject.Spec.Skill,
		"command":             cmdText,
		"exit_code":           runOutput.ExitCode,
		"latency_ms":          latency,
		"trace_path":          tracePath,
		"raw_trace_path":      rawTracePath,
		"raw_stdout_path":     rawStdoutPath,
		"raw_stderr_path":     rawStderrPath,
		"trajectory_path":     trajectoryPath,
		"workspace_diff_path": diffPath,
		"stdout":              trimText(runOutput.Stdout, 4000),
		"stderr":              trimText(runOutput.Stderr, 4000),
		"environment_setup":   environmentSetup,
		"preflight_checks":    preflightChecks,
		"sandbox_provider":    sandboxReq.Provider,
		"sandbox_image":       sandboxReq.DockerImage,
		"sandbox_fingerprint": trace.SandboxFingerprint,
		"token_source":        trace.TokenSource,
		"estimated_cost_usd":  trace.EstimatedCost,
		"cost_source":         trace.CostSource,
		"usage_source_detail": trace.UsageSourceDetail,
		"session_id":          trace.SessionID,
	}
	addTokenAccountingToMap(rawResponse, trace)
	if trace.SessionExportError != "" {
		rawResponse["session_export_error"] = trace.SessionExportError
	}
	if setupWarning != "" {
		rawResponse["environment_setup_warning"] = setupWarning
		trace.Stderr = trimText(strings.TrimSpace(trace.Stderr+"\nenvironment_setup_warning: "+setupWarning), 8000)
	}

	commandErrorDetail := ""
	if runErr != nil {
		commandErrorDetail = fmt.Sprintf("agent command failed: %s", summarizeAgentCommandError(runOutput.Stderr, runErr.Error(), 1000))
	}

	// 17. 拦截环境漂移行为
	if violation := detectSandboxPolicyViolation(trace.CommandsExecuted, frameworkForbiddenCommandPatterns(framework)); violation != "" {
		rawResponse["policy_violation"] = violation
		_ = writeAgentTrajectory(trajectoryPath, trace, "", violation, runOutput.Stdout, runOutput.Stderr)
		return AgentGenerateResult{
			RawResponse:       rawResponse,
			Trace:             trace,
			LatencyMS:         latency,
			PromptTokens:      trace.PromptTokens,
			CompletionTokens:  trace.CompletionTokens,
			TotalTokens:       trace.TotalTokens,
			RawInputTokens:    trace.RawInputTokens,
			CacheReadTokens:   trace.CacheReadTokens,
			CacheCreateTokens: trace.CacheCreateTokens,
			TokenSource:       trace.TokenSource,
			EstimatedCostUSD:  trace.EstimatedCost,
			CostSource:        trace.CostSource,
			Error: &contracts.ErrorInfo{
				Kind:      "sandbox_policy_error",
				Message:   violation,
				Retryable: false,
			},
		}
	}

	// 18. 查找生成的测试文件
	generatedPath := findGeneratedTest(workRoot, outputFile, framework.OutputGlobs, changes, sample.Language)
	if generatedPath == "" {
		errorKind := "agent_output_error"
		failureDetail := summarizeAgentCommandError(runOutput.Stderr, "", 1200)
		if commandErrorDetail != "" {
			errorKind = "agent_execution_error"
			failureDetail = commandErrorDetail
			rawResponse["agent_execution_error"] = failureDetail
		}
		if failureDetail == "" {
			failureDetail = tailText(strings.TrimSpace(runOutput.Stdout+"\n"+runOutput.Stderr), 1200)
		}
		if errorKind == "agent_output_error" {
			rawResponse["agent_output_error"] = failureDetail
		}
		_ = writeAgentTrajectory(trajectoryPath, trace, "", failureDetail, runOutput.Stdout, runOutput.Stderr)
		return AgentGenerateResult{
			RawResponse:       rawResponse,
			Trace:             trace,
			LatencyMS:         latency,
			PromptTokens:      trace.PromptTokens,
			CompletionTokens:  trace.CompletionTokens,
			TotalTokens:       trace.TotalTokens,
			RawInputTokens:    trace.RawInputTokens,
			CacheReadTokens:   trace.CacheReadTokens,
			CacheCreateTokens: trace.CacheCreateTokens,
			TokenSource:       trace.TokenSource,
			EstimatedCostUSD:  trace.EstimatedCost,
			CostSource:        trace.CostSource,
			Error: &contracts.ErrorInfo{
				Kind:      errorKind,
				Message:   buildNoGeneratedFileMessage(failureDetail),
				Retryable: false,
			},
		}
	}
	if commandErrorDetail != "" {
		rawResponse["agent_execution_warning"] = commandErrorDetail
	}

	raw, err := os.ReadFile(generatedPath)
	if err != nil {
		_ = writeAgentTrajectory(trajectoryPath, trace, "", err.Error(), runOutput.Stdout, runOutput.Stderr)
		return AgentGenerateResult{
			RawResponse:       rawResponse,
			Trace:             trace,
			LatencyMS:         latency,
			PromptTokens:      trace.PromptTokens,
			CompletionTokens:  trace.CompletionTokens,
			TotalTokens:       trace.TotalTokens,
			RawInputTokens:    trace.RawInputTokens,
			CacheReadTokens:   trace.CacheReadTokens,
			CacheCreateTokens: trace.CacheCreateTokens,
			TokenSource:       trace.TokenSource,
			EstimatedCostUSD:  trace.EstimatedCost,
			CostSource:        trace.CostSource,
			Error:             &contracts.ErrorInfo{Kind: "agent_output_error", Message: err.Error(), Retryable: false},
		}
	}
	code := strings.TrimSpace(string(raw))
	finalizeAgentAccounting(&trace, req.Prompt, code, req.Model)
	rawResponse["token_source"] = trace.TokenSource
	rawResponse["estimated_cost_usd"] = trace.EstimatedCost
	rawResponse["cost_source"] = trace.CostSource
	rawResponse["usage_source_detail"] = trace.UsageSourceDetail
	rawResponse["session_id"] = trace.SessionID
	rawResponse["session_export_path"] = trace.SessionExportPath
	addTokenAccountingToMap(rawResponse, trace)
	if trace.SessionExportError != "" {
		rawResponse["session_export_error"] = trace.SessionExportError
	}
	_ = writeAgentTrace(tracePath, trace)
	if err := validateGeneratedTest(code, sample.Language); err != nil {
		_ = writeAgentTrajectory(trajectoryPath, trace, generatedPath, err.Error(), runOutput.Stdout, runOutput.Stderr)
		return AgentGenerateResult{
			Code:              code,
			RawResponse:       rawResponse,
			Trace:             trace,
			LatencyMS:         latency,
			PromptTokens:      trace.PromptTokens,
			CompletionTokens:  trace.CompletionTokens,
			TotalTokens:       trace.TotalTokens,
			RawInputTokens:    trace.RawInputTokens,
			CacheReadTokens:   trace.CacheReadTokens,
			CacheCreateTokens: trace.CacheCreateTokens,
			TokenSource:       trace.TokenSource,
			EstimatedCostUSD:  trace.EstimatedCost,
			CostSource:        trace.CostSource,
			Error:             &contracts.ErrorInfo{Kind: "quality_error", Message: err.Error(), Retryable: false},
		}
	}

	rawResponse["generated_test_source_path"] = generatedPath
	rawResponse["generated_test_path"] = req.TestPath
	_ = writeAgentTrajectory(trajectoryPath, trace, generatedPath, "", runOutput.Stdout, runOutput.Stderr)

	return AgentGenerateResult{
		Code:              code,
		RawResponse:       rawResponse,
		Trace:             trace,
		LatencyMS:         latency,
		PromptTokens:      trace.PromptTokens,
		CompletionTokens:  trace.CompletionTokens,
		TotalTokens:       trace.TotalTokens,
		RawInputTokens:    trace.RawInputTokens,
		CacheReadTokens:   trace.CacheReadTokens,
		CacheCreateTokens: trace.CacheCreateTokens,
		TokenSource:       trace.TokenSource,
		EstimatedCostUSD:  trace.EstimatedCost,
		CostSource:        trace.CostSource,
	}
}

// parseAgentOutput 从 Agent 的 stdout/stderr 中解析结构化信息。
// 支持解析 OpenCode、Claude Code、CodeBuddy 等 CLI Agent 的日志格式。
func summarizeAgentCommandError(stderr, errText string, max int) string {
	combined := strings.TrimSpace(strings.TrimSpace(stderr) + "\n" + strings.TrimSpace(errText))
	if combined == "" {
		return ""
	}

	lines := strings.Split(combined, "\n")
	keywords := []string{
		"ERROR",
		"error:",
		"failed",
		"Unauthorized",
		"401",
		"403",
		"404",
		"timed out",
		"timeout",
	}
	picked := make([]string, 0, 8)
	for i := len(lines) - 1; i >= 0 && len(picked) < 8; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		for _, keyword := range keywords {
			if strings.Contains(line, keyword) {
				picked = append(picked, line)
				break
			}
		}
	}
	if len(picked) > 0 {
		for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
			picked[i], picked[j] = picked[j], picked[i]
		}
		return trimText(strings.Join(picked, "\n"), max)
	}

	return tailText(combined, max)
}

func buildNoGeneratedFileMessage(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return "agent did not create a generated test file"
	}
	return "agent did not create a generated test file: " + trimText(detail, 1200)
}

func shouldFallbackCLIAgentToModelAPI(req AgentGenerateRequest, trace AgentTrace, message string, runErr error) bool {
	if !strings.EqualFold(req.Subject.Spec.Kind, "cli_agent") || runErr != nil {
		return false
	}
	if strings.TrimSpace(trace.Framework) != "" && !strings.EqualFold(trace.Framework, req.Subject.Spec.Framework) {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	if msg == "" {
		return false
	}
	return (strings.Contains(msg, "did not produce") || strings.Contains(msg, "did not create")) &&
		(strings.Contains(msg, "test file") || strings.Contains(msg, "generated test"))
}

func tailText(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[len(value)-max:]
}

func addTokenAccountingToMap(out map[string]any, trace AgentTrace) {
	if out == nil {
		return
	}
	out["prompt_tokens"] = trace.PromptTokens
	out["completion_tokens"] = trace.CompletionTokens
	out["total_tokens"] = trace.TotalTokens
	out["raw_input_tokens"] = trace.RawInputTokens
	out["cache_read_input_tokens"] = trace.CacheReadTokens
	out["cache_creation_input_tokens"] = trace.CacheCreateTokens
}

func parseAgentOutput(trace *AgentTrace, stdout, stderr string) {
	fullOutput := stdout + "\n" + stderr

	trace.CommandsExecuted = parseCommands(fullOutput)

	// 解析工具调用（根据框架选择不同解析策略）
	switch {
	case strings.EqualFold(trace.Framework, "codebuddy"):
		trace.ToolCalls = parseCodeBuddyToolCalls(stdout, stderr)
	case strings.EqualFold(trace.Framework, "claudecode"), strings.EqualFold(trace.Framework, "claude_code"), strings.EqualFold(trace.Framework, "claude-code"):
		trace.ToolCalls = parseClaudeCodeToolCalls(stdout, stderr)
	default:
		trace.ToolCalls = parseToolCalls(fullOutput)
	}
	trace.CommandsExecuted = mergeCommands(trace.CommandsExecuted, commandsFromToolCalls(trace.ToolCalls))

	// 解析文件读写
	trace.FilesRead = parseFileReads(trace.CommandsExecuted, trace.Command)
	trace.FilesWritten = parseFileWrites(trace.CommandsExecuted, trace.WorkspaceDiff)

	// 尝试从结构化输出/日志中解析 token 用量与 session 信息
	parseUsageAndSession(trace, stdout, stderr)

	finalizeInteractionCount(trace)
}

type usageRecord struct {
	Prompt      *int
	Completion  *int
	Total       *int
	RawInput    *int
	CacheRead   *int
	CacheCreate *int
}

// parseToolCalls 从输出中解析工具调用记录。
func parseToolCalls(output string) []ToolCall {
	var calls []ToolCall
	seen := map[string]struct{}{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		match := toolStartPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		tool := strings.TrimSpace(match[1])
		if tool == "" {
			continue
		}
		key := tool + "|" + line
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		calls = append(calls, ToolCall{
			Tool:    tool,
			Input:   trimText(line, 500),
			Success: true,
		})
	}
	return calls
}

// parseFileReads 从输出中解析 Agent 读取的文件列表。
func parseFileReads(commands []string, topLevelCommand string) []string {
	seen := map[string]struct{}{}
	var files []string
	for _, line := range append([]string{topLevelCommand}, commands...) {
		for _, candidate := range extractRelevantPaths(line) {
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			files = append(files, candidate)
		}
	}
	sort.Strings(files)
	return files
}

// parseFileWrites 从输出中解析 Agent 写入的文件列表。
func parseFileWrites(commands []string, workspaceDiff []string) []string {
	seen := map[string]struct{}{}
	var files []string

	// 从 workspace diff 中获取（最可靠）
	for _, f := range workspaceDiff {
		if !isRelevantWorkspacePath(f) {
			continue
		}
		if _, ok := seen[f]; !ok {
			seen[f] = struct{}{}
			files = append(files, f)
		}
	}

	// 从命令文本中补充工作区内的高置信度路径。
	for _, line := range commands {
		for _, candidate := range extractRelevantPaths(line) {
			if !isRelevantWorkspaceWritePath(candidate) {
				continue
			}
			if _, ok := seen[candidate]; !ok {
				seen[candidate] = struct{}{}
				files = append(files, candidate)
			}
		}
	}
	sort.Strings(files)
	return files
}

// parseCommands 从输出中解析 Agent 执行的 shell 命令。
func parseCommands(output string) []string {
	var commands []string
	seen := map[string]struct{}{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if match := bashPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			cmd := strings.TrimSpace(match[1])
			if !looksLikeRealCommand(cmd) {
				continue
			}
			if _, ok := seen[cmd]; ok {
				continue
			}
			seen[cmd] = struct{}{}
			commands = append(commands, trimText(cmd, 500))
		}
	}
	return commands
}

func commandsFromToolCalls(calls []ToolCall) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, call := range calls {
		tool := strings.ToLower(strings.TrimSpace(call.Tool))
		if tool != "bash" && tool != "shell" && tool != "sh" && tool != "powershell" {
			continue
		}
		cmd := extractCommandFromToolInput(call.Input)
		if cmd == "" || !looksLikeRealCommand(cmd) {
			continue
		}
		cmd = trimText(cmd, 500)
		if _, ok := seen[cmd]; ok {
			continue
		}
		seen[cmd] = struct{}{}
		out = append(out, cmd)
	}
	return out
}

func extractCommandFromToolInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err == nil {
		for _, key := range []string{"command", "cmd", "script"} {
			if raw, ok := payload[key]; ok {
				if cmd := strings.TrimSpace(fmt.Sprint(raw)); cmd != "" {
					return cmd
				}
			}
		}
	}
	return strings.TrimSpace(input)
}

func mergeCommands(groups ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, group := range groups {
		for _, cmd := range group {
			cmd = strings.TrimSpace(cmd)
			if cmd == "" {
				continue
			}
			if _, ok := seen[cmd]; ok {
				continue
			}
			seen[cmd] = struct{}{}
			out = append(out, cmd)
		}
	}
	return out
}

func finalizeInteractionCount(trace *AgentTrace) {
	if trace == nil {
		return
	}
	if trace.InteractionCount > 0 {
		return
	}
	if len(trace.ToolCalls) > 0 {
		trace.InteractionCount = len(trace.ToolCalls)
		return
	}
	trace.InteractionCount = 1
}

func runSandboxPreflight(ctx context.Context, sandboxRunner SandboxRunner, baseReq SandboxRunRequest, commands []string) ([]PreflightCheck, error) {
	checks := make([]PreflightCheck, 0, len(commands))
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		req := baseReq
		req.Command = command
		started := time.Now()
		out, err := sandboxRunner.Run(ctx, req)
		check := PreflightCheck{
			Command:    command,
			ExitCode:   out.ExitCode,
			DurationMS: int(time.Since(started).Milliseconds()),
			Stdout:     trimText(out.Stdout, 1000),
			Stderr:     trimText(out.Stderr, 1000),
			Passed:     err == nil && out.ExitCode == 0,
		}
		checks = append(checks, check)
		if !check.Passed {
			message := trimText(strings.TrimSpace(out.Stderr+"\n"+out.Stdout), 500)
			if message == "" && err != nil {
				message = err.Error()
			}
			return checks, fmt.Errorf("sandbox preflight failed for %q: %s", command, message)
		}
	}
	return checks, nil
}

func detectSandboxPolicyViolation(commands []string, patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		for _, command := range commands {
			if strings.Contains(strings.ToLower(command), pattern) {
				return fmt.Sprintf("sandbox policy violation: attempted forbidden environment mutation command %q", command)
			}
		}
	}
	return ""
}

// looksLikeFilePath 判断一个字符串是否像文件路径。
func looksLikeFilePath(s string) bool {
	if len(s) < 3 || len(s) > 500 {
		return false
	}
	if strings.ContainsAny(s, "\r\n\t ") {
		return false
	}
	// 包含路径分隔符
	if strings.Contains(s, "/") || strings.Contains(s, "\\") {
		// 包含文件扩展名
		ext := filepath.Ext(s)
		return ext != "" && len(ext) <= 6
	}
	return false
}

// writeAgentTrace 将 Agent trace 写成真正的 JSONL 事件流。
func writeAgentTrace(path string, trace AgentTrace) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	emit := func(event string, payload map[string]any) error {
		payload["schema_version"] = "agent_trace.v0.2.0"
		payload["event"] = event
		return enc.Encode(payload)
	}

	if err := emit("summary", map[string]any{
		"subject_id":                  trace.SubjectID,
		"framework":                   trace.Framework,
		"model":                       trace.Model,
		"skill":                       trace.Skill,
		"sample_id":                   trace.SampleID,
		"language":                    trace.Language,
		"started_at":                  trace.StartedAt,
		"finished_at":                 trace.FinishedAt,
		"session_id":                  trace.SessionID,
		"session_export_path":         trace.SessionExportPath,
		"session_export_error":        trace.SessionExportError,
		"raw_trace_path":              trace.RawTracePath,
		"raw_stdout_path":             trace.RawStdoutPath,
		"raw_stderr_path":             trace.RawStderrPath,
		"trajectory_path":             trace.TrajectoryPath,
		"workspace_diff_path":         trace.WorkspaceDiffPath,
		"sandbox_provider":            trace.SandboxProvider,
		"sandbox_image":               trace.SandboxImage,
		"sandbox_fingerprint":         trace.SandboxFingerprint,
		"interaction_count":           trace.InteractionCount,
		"tool_call_count":             len(trace.ToolCalls),
		"commands_count":              len(trace.CommandsExecuted),
		"files_read_count":            len(trace.FilesRead),
		"files_written_count":         len(trace.FilesWritten),
		"prompt_tokens":               trace.PromptTokens,
		"completion_tokens":           trace.CompletionTokens,
		"total_tokens":                trace.TotalTokens,
		"raw_input_tokens":            trace.RawInputTokens,
		"cache_read_input_tokens":     trace.CacheReadTokens,
		"cache_creation_input_tokens": trace.CacheCreateTokens,
		"token_source":                trace.TokenSource,
		"estimated_cost":              trace.EstimatedCost,
		"cost_source":                 trace.CostSource,
		"usage_source_detail":         trace.UsageSourceDetail,
	}); err != nil {
		return err
	}

	for i, check := range trace.EnvironmentSetup {
		if err := emit("environment_setup", preflightTracePayload(i+1, check)); err != nil {
			return err
		}
	}
	for i, check := range trace.PreflightChecks {
		if err := emit("preflight_check", preflightTracePayload(i+1, check)); err != nil {
			return err
		}
	}
	for i, call := range trace.ToolCalls {
		if err := emit("tool_call", map[string]any{
			"index":       i + 1,
			"tool":        call.Tool,
			"input":       trimText(call.Input, 4000),
			"output":      trimText(call.Output, 4000),
			"duration_ms": call.DurationMS,
			"success":     call.Success,
		}); err != nil {
			return err
		}
	}
	for i, command := range trace.CommandsExecuted {
		if err := emit("command", map[string]any{
			"index":   i + 1,
			"command": command,
		}); err != nil {
			return err
		}
	}
	for i, file := range trace.FilesRead {
		if err := emit("file_read", map[string]any{"index": i + 1, "path": file}); err != nil {
			return err
		}
	}
	for i, file := range trace.FilesWritten {
		if err := emit("file_written", map[string]any{"index": i + 1, "path": file}); err != nil {
			return err
		}
	}

	return emit("outcome", map[string]any{
		"exit_code":            trace.ExitCode,
		"duration_ms":          trace.DurationMS,
		"workspace_diff":       compactStringList(trace.WorkspaceDiff, 200, 1000),
		"workspace_diff_count": len(trace.WorkspaceDiff),
		"stdout_excerpt":       trimText(trace.Stdout, 4000),
		"stderr_excerpt":       trimText(trace.Stderr, 4000),
	})
}

func preflightTracePayload(index int, check PreflightCheck) map[string]any {
	return map[string]any{
		"index":       index,
		"command":     check.Command,
		"exit_code":   check.ExitCode,
		"duration_ms": check.DurationMS,
		"stdout":      trimText(check.Stdout, 4000),
		"stderr":      trimText(check.Stderr, 4000),
		"passed":      check.Passed,
	}
}

// agentError 构建一个包含错误的 AgentGenerateResult。
func agentError(kind string, err error) AgentGenerateResult {
	return AgentGenerateResult{
		Error: &contracts.ErrorInfo{
			Kind:      kind,
			Message:   err.Error(),
			Retryable: false,
		},
	}
}

var (
	sessionIDPattern = regexp.MustCompile(`session id=([A-Za-z0-9_\-]+)`)
	pathPattern      = regexp.MustCompile("(?:path=|to\\s+|from\\s+|at\\s+|`)([/\\\\][^\\s\"'`,:;()]+(?:\\.[A-Za-z0-9]{1,8})?)")
	toolStartPattern = regexp.MustCompile(`service=tool\.registry\s+status=started\s+([A-Za-z0-9._-]+)`)
	bashPattern      = regexp.MustCompile(`permission=bash pattern=(.+?)(?:\s+ruleset=|\s+action=|\s+evaluated|$)`)
	extTokenPattern  = regexp.MustCompile("(?:^|[\\s\"'`=])((?:/workspace/)?[A-Za-z0-9_./-]+\\.(?:py|go|java|cpp|cc|cxx|h|hpp|md|txt|json|ya?ml))(?:$|[\\s\"'`,:;()])")
)

// parseCodeBuddyJSONOutput 尝试从 CodeBuddy Code 的 --output-format json 输出中
// 解析 session_id 和 token usage。
// CodeBuddy 在 -p 模式下输出 JSON（可能是对象或数组），包含消息和 usage 等字段。
func parseCodeBuddyJSONOutput(trace *AgentTrace, stdout string) {
	parseStructuredAgentJSONOutput(trace, stdout, "codebuddy_json_output")
}

// accumulateUsage 将 usageRecord 累加到 trace 的 token 字段。
func accumulateUsage(trace *AgentTrace, records []usageRecord) {
	var promptSum, completionSum, totalSum int
	var rawInputSum, cacheReadSum, cacheCreateSum int
	var promptSeen, completionSeen, totalSeen bool
	var rawInputSeen, cacheReadSeen, cacheCreateSeen bool
	for _, record := range records {
		if record.Prompt != nil {
			promptSum += *record.Prompt
			promptSeen = true
		}
		if record.Completion != nil {
			completionSum += *record.Completion
			completionSeen = true
		}
		if record.Total != nil {
			totalSum += *record.Total
			totalSeen = true
		}
		if record.RawInput != nil {
			rawInputSum += *record.RawInput
			rawInputSeen = true
		}
		if record.CacheRead != nil {
			cacheReadSum += *record.CacheRead
			cacheReadSeen = true
		}
		if record.CacheCreate != nil {
			cacheCreateSum += *record.CacheCreate
			cacheCreateSeen = true
		}
	}
	if promptSeen && trace.PromptTokens == nil {
		trace.PromptTokens = intPtr(promptSum)
	}
	if completionSeen && trace.CompletionTokens == nil {
		trace.CompletionTokens = intPtr(completionSum)
	}
	if totalSeen && trace.TotalTokens == nil {
		trace.TotalTokens = intPtr(totalSum)
	}
	if trace.TotalTokens == nil && trace.PromptTokens != nil && trace.CompletionTokens != nil {
		trace.TotalTokens = intPtr(*trace.PromptTokens + *trace.CompletionTokens)
	}
	if rawInputSeen && trace.RawInputTokens == nil {
		trace.RawInputTokens = intPtr(rawInputSum)
	}
	if cacheReadSeen && trace.CacheReadTokens == nil {
		trace.CacheReadTokens = intPtr(cacheReadSum)
	}
	if cacheCreateSeen && trace.CacheCreateTokens == nil {
		trace.CacheCreateTokens = intPtr(cacheCreateSum)
	}
}

// parseCodeBuddyJSONLUsage 从 CodeBuddy 的 stream-json (JSONL) 输出行中累加 token usage。
func parseCodeBuddyJSONLUsage(trace *AgentTrace, output string) {
	parseStructuredAgentJSONLUsage(trace, output, "codebuddy_jsonl_lines")
}

// parseCodeBuddyToolCalls 从 CodeBuddy 的 stream-json (JSONL) 输出中解析工具调用。
// 每行是一个 JSON 对象，助手消息的 content 数组中包含 type:"tool_use" 的块。
func parseCodeBuddyToolCalls(stdout, stderr string) []ToolCall {
	return parseStructuredJSONToolCalls(stdout, stderr)
}

func parseClaudeCodeJSONOutput(trace *AgentTrace, stdout string) {
	parseStructuredAgentJSONOutput(trace, stdout, "claudecode_json_output")
}

func parseClaudeCodeJSONLUsage(trace *AgentTrace, output string) {
	parseStructuredAgentJSONLUsage(trace, output, "claudecode_jsonl_lines")
}

func parseClaudeCodeToolCalls(stdout, stderr string) []ToolCall {
	return parseStructuredJSONToolCalls(stdout, stderr)
}

func parseStructuredAgentJSONOutput(trace *AgentTrace, stdout, usageDetail string) {
	jsonStr := extractFinalJSON(stdout)
	if jsonStr == "" {
		return
	}

	var payload any
	if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
		return
	}

	assignSessionID(trace, payload)
	assignInteractionCount(trace, payload)
	records := preferredUsageRecords(payload)
	if len(records) == 0 {
		return
	}
	accumulateUsage(trace, records)
	trace.TokenSource = "actual"
	trace.UsageSourceDetail = usageDetail
}

func parseStructuredAgentJSONLUsage(trace *AgentTrace, output, usageDetail string) {
	var records []usageRecord

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}
		assignSessionID(trace, payload)
		assignInteractionCount(trace, payload)
		if typ, _ := payload["type"].(string); typ == "result" {
			if usage, ok := payload["usage"].(map[string]any); ok {
				records = append(records, extractUsageRecords(usage)...)
				continue
			}
		}
		records = append(records, preferredUsageRecords(payload)...)
	}
	if len(records) > 0 {
		accumulateUsage(trace, records)
		trace.TokenSource = "actual"
		trace.UsageSourceDetail = usageDetail
	}
}

func parseStructuredJSONToolCalls(stdout, stderr string) []ToolCall {
	var calls []ToolCall
	seen := map[string]struct{}{}

	for _, output := range []string{stdout, stderr} {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}

			// stream-json 格式：助手消息包含 content 数组
			// {"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"bash","input":{...}}]}}
			msg, _ := event["message"].(map[string]any)
			if msg == nil {
				msg = event // 兼容非嵌套格式
			}
			content, _ := msg["content"].([]any)
			for _, block := range content {
				blockMap, ok := block.(map[string]any)
				if !ok {
					continue
				}
				blockType, _ := blockMap["type"].(string)
				if blockType != "tool_use" && blockType != "function_call" {
					continue
				}
				tool, _ := blockMap["name"].(string)
				if tool == "" {
					continue
				}
				key := tool + "|" + line
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}

				input := ""
				if args, ok := blockMap["input"]; ok {
					if b, err := json.Marshal(args); err == nil {
						input = trimText(string(b), 500)
					}
				}
				calls = append(calls, ToolCall{
					Tool:    tool,
					Input:   input,
					Success: true,
				})
			}

			// 兼容：直接在 event 层级的工具字段
			if tool := extractToolName(event); tool != "" {
				key := tool + "|" + line
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					calls = append(calls, ToolCall{Tool: tool, Success: true})
				}
			}
		}
	}
	return calls
}

func assignSessionID(trace *AgentTrace, value any) {
	if trace == nil || trace.SessionID != "" {
		return
	}
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"session_id", "sessionId"} {
			if sid, ok := v[key]; ok {
				if s, ok := sid.(string); ok && s != "" {
					trace.SessionID = s
					return
				}
			}
		}
		for _, child := range v {
			assignSessionID(trace, child)
			if trace.SessionID != "" {
				return
			}
		}
	case []any:
		for _, child := range v {
			assignSessionID(trace, child)
			if trace.SessionID != "" {
				return
			}
		}
	}
}

func assignInteractionCount(trace *AgentTrace, value any) {
	if trace == nil || trace.InteractionCount > 0 {
		return
	}
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"num_turns", "numTurns", "turns", "interaction_count", "interactionCount"} {
			if raw, ok := v[key]; ok {
				if n, ok := asInt(raw); ok && n > 0 {
					trace.InteractionCount = n
					return
				}
			}
		}
		for _, child := range v {
			assignInteractionCount(trace, child)
			if trace.InteractionCount > 0 {
				return
			}
		}
	case []any:
		for _, child := range v {
			assignInteractionCount(trace, child)
			if trace.InteractionCount > 0 {
				return
			}
		}
	}
}

// extractToolName 从 CodeBuddy JSON 事件中提取工具名称。
// 支持多种常见字段名和嵌套结构。
func extractToolName(event map[string]any) string {
	// 直接字段
	for _, key := range []string{"tool", "tool_name", "name"} {
		if v, ok := event[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				// 过滤掉非工具类型的事件
				lower := strings.ToLower(s)
				if lower == "result" || lower == "message" || lower == "error" || lower == "status" {
					continue
				}
				return s
			}
		}
	}
	// type 字段中提取（如 "tool_use", "tool_call"）
	if typ, ok := event["type"]; ok {
		if s, ok := typ.(string); ok {
			lower := strings.ToLower(s)
			if strings.Contains(lower, "tool") {
				// 尝试从 function.name 或 name 获取
				if fn, ok := event["function"]; ok {
					if fnMap, ok := fn.(map[string]any); ok {
						if n, ok := fnMap["name"]; ok {
							if name, ok := n.(string); ok {
								return name
							}
						}
					}
				}
			}
		}
	}
	return ""
}

// extractFinalJSON 从可能包含混合文本+JSON的输出中提取最后一个完整 JSON 值（对象或数组）。
func extractFinalJSON(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	// 快速路径：整个输出就是合法 JSON
	if json.Valid([]byte(output)) {
		return output
	}
	// 从末尾向前搜索，尝试找到最后一个可解析的 JSON 对象或数组
	lastObj := strings.LastIndex(output, "{")
	lastArr := strings.LastIndex(output, "[")
	start := lastObj
	if lastArr > start {
		start = lastArr
	}
	if start < 0 {
		return ""
	}
	for i := start; i >= 0; i-- {
		c := output[i]
		if c != '{' && c != '[' {
			continue
		}
		candidate := output[i:]
		if json.Valid([]byte(candidate)) {
			return candidate
		}
		var payload any
		if json.Unmarshal([]byte(candidate), &payload) == nil {
			return candidate
		}
	}
	return ""
}

func parseUsageAndSession(trace *AgentTrace, stdout, stderr string) {
	fullOutput := stdout + "\n" + stderr

	// CodeBuddy / Claude Code: 优先从 JSON 输出中提取精确数据。
	// 同时检查 stdout 和 stderr，因为框架可能将结果输出到任一流。
	if strings.EqualFold(trace.Framework, "codebuddy") {
		parseCodeBuddyJSONOutput(trace, stdout)
		if trace.PromptTokens == nil && trace.CompletionTokens == nil && trace.TotalTokens == nil {
			parseCodeBuddyJSONOutput(trace, stderr)
		}
		// 如果 session 或 usage 仍不完整，再尝试从 JSONL 行中补齐
		if trace.SessionID == "" || trace.PromptTokens == nil && trace.CompletionTokens == nil && trace.TotalTokens == nil {
			parseCodeBuddyJSONLUsage(trace, fullOutput)
		}
	} else if strings.EqualFold(trace.Framework, "claudecode") || strings.EqualFold(trace.Framework, "claude_code") || strings.EqualFold(trace.Framework, "claude-code") {
		parseClaudeCodeJSONOutput(trace, stdout)
		if trace.PromptTokens == nil && trace.CompletionTokens == nil && trace.TotalTokens == nil {
			parseClaudeCodeJSONOutput(trace, stderr)
		}
		if trace.SessionID == "" || trace.PromptTokens == nil && trace.CompletionTokens == nil && trace.TotalTokens == nil {
			parseClaudeCodeJSONLUsage(trace, fullOutput)
		}
	}

	if trace.SessionID == "" {
		if match := sessionIDPattern.FindStringSubmatch(fullOutput); len(match) == 2 {
			trace.SessionID = match[1]
		}
	}

	records := collectUsageRecords(fullOutput)
	if len(records) == 0 {
		return
	}
	accumulateUsage(trace, records)
	if trace.TokenSource == "" {
		trace.TokenSource = "actual"
	}
	if strings.TrimSpace(trace.UsageSourceDetail) == "" {
		trace.UsageSourceDetail = "log_json"
	}
}

func collectOpenCodeSessionExport(
	ctx context.Context,
	sandboxRunner SandboxRunner,
	sandboxReq SandboxRunRequest,
	workRoot string,
	traceDir string,
	sampleID string,
	trace *AgentTrace,
) {
	if trace == nil || !strings.EqualFold(trace.Framework, "opencode") {
		return
	}
	if strings.TrimSpace(trace.SessionID) == "" {
		return
	}

	exportWorkspaceRel := filepath.Join(".utbench", "opencode", "session_export.json")
	exportHostPath := filepath.Join(workRoot, exportWorkspaceRel)
	exportContainerPath := "/workspace/" + filepath.ToSlash(exportWorkspaceRel)
	sessionExportPath := filepath.Join(traceDir, sampleID+".session_export.json")

	exportReq := sandboxReq
	exportReq.Command = fmt.Sprintf(
		"mkdir -p /workspace/.utbench/opencode && opencode export %s > %s",
		shQuote(trace.SessionID),
		shQuote(exportContainerPath),
	)
	if exportReq.TimeoutSeconds <= 0 || exportReq.TimeoutSeconds > 120 {
		exportReq.TimeoutSeconds = 120
	}

	result, err := sandboxRunner.Run(ctx, exportReq)
	if err != nil {
		trace.SessionExportError = fmt.Sprintf("export command failed: %v", err)
		return
	}
	if result.ExitCode != 0 {
		trace.SessionExportError = strings.TrimSpace(result.Stderr)
		if trace.SessionExportError == "" {
			trace.SessionExportError = fmt.Sprintf("export command exited with code %d", result.ExitCode)
		}
		return
	}

	raw, err := os.ReadFile(exportHostPath)
	if err != nil {
		trace.SessionExportError = fmt.Sprintf("read session export: %v", err)
		return
	}
	if err := os.WriteFile(sessionExportPath, raw, 0o644); err != nil {
		trace.SessionExportError = fmt.Sprintf("persist session export: %v", err)
		return
	}
	trace.SessionExportPath = sessionExportPath

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		trace.SessionExportError = fmt.Sprintf("parse session export json: %v", err)
		return
	}

	records := deduplicateUsageRecords(extractUsageRecords(payload))
	if len(records) > 0 {
		// OpenCode 的 tokens.total 是累积值（从会话开始到当前消息的总 token），
		// 不能直接求和；prompt/completion (input/output) 是单次值，可以求和。
		var promptSum, completionSum, rawInputSum, cacheReadSum, cacheCreateSum int
		var maxTotal int
		var promptSeen, completionSeen, totalSeen bool
		var rawInputSeen, cacheReadSeen, cacheCreateSeen bool
		for _, record := range records {
			if record.Prompt != nil {
				promptSum += *record.Prompt
				promptSeen = true
			}
			if record.Completion != nil {
				completionSum += *record.Completion
				completionSeen = true
			}
			if record.Total != nil {
				totalSeen = true
				if *record.Total > maxTotal {
					maxTotal = *record.Total
				}
			}
			if record.RawInput != nil {
				rawInputSum += *record.RawInput
				rawInputSeen = true
			}
			if record.CacheRead != nil {
				cacheReadSum += *record.CacheRead
				cacheReadSeen = true
			}
			if record.CacheCreate != nil {
				cacheCreateSum += *record.CacheCreate
				cacheCreateSeen = true
			}
		}
		if promptSeen {
			trace.PromptTokens = intPtr(promptSum)
		}
		if completionSeen {
			trace.CompletionTokens = intPtr(completionSum)
		}
		if promptSeen && completionSeen {
			trace.TotalTokens = intPtr(promptSum + completionSum)
		} else if totalSeen {
			trace.TotalTokens = intPtr(maxTotal)
		}
		if rawInputSeen {
			trace.RawInputTokens = intPtr(rawInputSum)
		}
		if cacheReadSeen {
			trace.CacheReadTokens = intPtr(cacheReadSum)
		}
		if cacheCreateSeen {
			trace.CacheCreateTokens = intPtr(cacheCreateSum)
		}
		trace.TokenSource = "actual"
		trace.UsageSourceDetail = "opencode_session_export"
	}

	// 从 session export 中提取实际的工具调用（替换从 stdout/stderr 解析的假工具调用）
	toolCalls := extractOpenCodeToolCalls(payload)
	if len(toolCalls) > 0 {
		trace.ToolCalls = toolCalls
	}
	if turns := countOpenCodeAssistantMessages(payload); turns > 0 {
		trace.InteractionCount = turns
	}
	trace.CommandsExecuted = mergeCommands(trace.CommandsExecuted, commandsFromToolCalls(trace.ToolCalls))
	trace.FilesRead = parseFileReads(trace.CommandsExecuted, trace.Command)
	trace.FilesWritten = parseFileWrites(trace.CommandsExecuted, trace.WorkspaceDiff)
}

// extractOpenCodeToolCalls 从 OpenCode session export 中提取实际的工具调用。
// session export 的 messages 数组中，每个 message 包含 parts 数组，
// 其中 type="tool" 的 part 表示实际的工具调用。
func extractOpenCodeToolCalls(payload any) []ToolCall {
	var calls []ToolCall
	seen := map[string]struct{}{}

	payloadMap, ok := payload.(map[string]any)
	if !ok {
		return calls
	}

	messages, ok := payloadMap["messages"].([]any)
	if !ok {
		return calls
	}

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		parts, ok := msgMap["parts"].([]any)
		if !ok {
			continue
		}

		for _, part := range parts {
			partMap, ok := part.(map[string]any)
			if !ok {
				continue
			}

			// 只处理 type="tool" 的部分（实际的工具调用）
			partType, _ := partMap["type"].(string)
			if partType != "tool" {
				continue
			}

			tool, _ := partMap["tool"].(string)
			if tool == "" {
				continue
			}

			// 提取输入信息
			input := ""
			if state, ok := partMap["state"].(map[string]any); ok {
				if inputMap, ok := state["input"].(map[string]any); ok {
					if b, err := json.Marshal(inputMap); err == nil {
						input = trimText(string(b), 500)
					}
				}
			}

			// 使用 tool + callID 作为去重键
			callID, _ := partMap["callID"].(string)
			key := tool + "|" + callID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			// 检查是否成功完成
			success := true
			if state, ok := partMap["state"].(map[string]any); ok {
				if status, ok := state["status"].(string); ok {
					success = status == "completed"
				}
			}

			calls = append(calls, ToolCall{
				Tool:    tool,
				Input:   input,
				Success: success,
			})
		}
	}

	return calls
}

func countOpenCodeAssistantMessages(payload any) int {
	payloadMap, ok := payload.(map[string]any)
	if !ok {
		return 0
	}
	messages, ok := payloadMap["messages"].([]any)
	if !ok {
		return 0
	}
	count := 0
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		info, _ := msgMap["info"].(map[string]any)
		role, _ := info["role"].(string)
		if strings.EqualFold(role, "assistant") {
			count++
			continue
		}
		role, _ = msgMap["role"].(string)
		if strings.EqualFold(role, "assistant") {
			count++
		}
	}
	return count
}

func collectUsageRecords(output string) []usageRecord {
	lines := strings.Split(output, "\n")
	seen := map[string]struct{}{}
	var out []usageRecord
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}
		records := extractUsageRecords(payload)
		for _, record := range records {
			keyBytes, _ := json.Marshal(record)
			key := string(keyBytes)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, record)
		}
	}
	return out
}

func deduplicateUsageRecords(records []usageRecord) []usageRecord {
	seen := map[string]struct{}{}
	var out []usageRecord
	for _, record := range records {
		keyBytes, _ := json.Marshal(record)
		key := string(keyBytes)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, record)
	}
	return out
}

func preferredUsageRecords(value any) []usageRecord {
	if payload, ok := value.(map[string]any); ok {
		if usage, ok := payload["usage"].(map[string]any); ok {
			return extractUsageRecords(usage)
		}
	}
	return extractUsageRecords(value)
}

func extractUsageRecords(value any) []usageRecord {
	switch v := value.(type) {
	case map[string]any:
		var out []usageRecord
		if record, ok := usageRecordFromMap(v); ok {
			out = append(out, record)
		}
		for _, child := range v {
			out = append(out, extractUsageRecords(child)...)
		}
		return out
	case []any:
		var out []usageRecord
		for _, child := range v {
			out = append(out, extractUsageRecords(child)...)
		}
		return out
	default:
		return nil
	}
}

func usageRecordFromMap(v map[string]any) (usageRecord, bool) {
	rawInput := firstIntValue(v, "input_tokens", "inputTokens")
	prompt := firstIntValue(v, "prompt_tokens", "input_tokens", "inputTokens", "promptTokens")
	completion := firstIntValue(v, "completion_tokens", "output_tokens", "outputTokens", "completionTokens")
	total := firstIntValue(v, "total_tokens", "totalTokens")
	cacheRead := firstIntValue(v, "cache_read_input_tokens", "cacheReadInputTokens")
	cacheCreate := firstIntValue(v, "cache_creation_input_tokens", "cacheCreationInputTokens")
	if (prompt == nil || completion == nil || total == nil) && v["tokens"] != nil {
		if nested, ok := v["tokens"].(map[string]any); ok {
			if prompt == nil {
				prompt = firstIntValue(nested, "input", "input_tokens", "prompt", "prompt_tokens")
			}
			if completion == nil {
				completion = firstIntValue(nested, "output", "output_tokens", "completion", "completion_tokens")
			}
			if total == nil {
				total = firstIntValue(nested, "total", "total_tokens")
			}
			if cacheRead == nil {
				cacheRead = nestedCacheToken(nested, "read")
			}
			if cacheCreate == nil {
				cacheCreate = nestedCacheToken(nested, "write", "create", "creation")
			}
		}
	}

	// 处理缓存 token：CodeBuddy/Claude 的 input_tokens 通常包含缓存读取 token，
	// 净 token 口径需要扣掉 cache_read；OpenCode 的 tokens.input 已经是净值，只记录 cache。
	if prompt != nil && rawInput != nil {
		if cacheRead != nil && *cacheRead > 0 && *prompt >= *cacheRead {
			actualPrompt := *prompt - *cacheRead
			prompt = &actualPrompt
		}
	}

	if prompt == nil && completion == nil && total == nil {
		return usageRecord{}, false
	}
	if total == nil && prompt != nil && completion != nil {
		total = intPtr(*prompt + *completion)
	}
	return usageRecord{Prompt: prompt, Completion: completion, Total: total, RawInput: rawInput, CacheRead: cacheRead, CacheCreate: cacheCreate}, true
}

func nestedCacheToken(tokens map[string]any, keys ...string) *int {
	cache, ok := tokens["cache"].(map[string]any)
	if !ok {
		return nil
	}
	return firstIntValue(cache, keys...)
}

func firstIntValue(v map[string]any, keys ...string) *int {
	for _, key := range keys {
		raw, ok := v[key]
		if !ok {
			continue
		}
		if out, ok := asInt(raw); ok {
			return intPtr(out)
		}
	}
	return nil
}

func asInt(v any) (int, bool) {
	switch value := v.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	case json.Number:
		i, err := value.Int64()
		return int(i), err == nil
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return 0, false
		}
		i, err := strconv.Atoi(value)
		return i, err == nil
	default:
		return 0, false
	}
}

func finalizeAgentAccounting(trace *AgentTrace, prompt string, generated string, model modelConfig) {
	sourceBefore := strings.ToLower(strings.TrimSpace(trace.TokenSource))
	addedEstimate := estimateMissingTokenUsage(trace, prompt, generated)

	switch {
	case trace.PromptTokens != nil && trace.CompletionTokens != nil:
		if trace.TotalTokens == nil {
			trace.TotalTokens = intPtr(*trace.PromptTokens + *trace.CompletionTokens)
		}
		if strings.TrimSpace(trace.TokenSource) == "" {
			if addedEstimate {
				trace.TokenSource = "estimated"
			} else {
				trace.TokenSource = "actual"
			}
		} else if sourceBefore == "actual" && addedEstimate {
			trace.TokenSource = "partial"
		}
		if strings.TrimSpace(trace.UsageSourceDetail) == "" {
			if addedEstimate {
				trace.UsageSourceDetail = "prompt_and_generated_test_heuristic"
			} else {
				trace.UsageSourceDetail = "agent_usage"
			}
		}
	case trace.PromptTokens != nil || trace.CompletionTokens != nil || trace.TotalTokens != nil:
		if trace.TotalTokens == nil && trace.PromptTokens != nil && trace.CompletionTokens != nil {
			trace.TotalTokens = intPtr(*trace.PromptTokens + *trace.CompletionTokens)
		}
		if strings.TrimSpace(trace.TokenSource) == "" {
			if addedEstimate {
				trace.TokenSource = "estimated"
			} else {
				trace.TokenSource = "partial"
			}
		} else if sourceBefore == "actual" && addedEstimate {
			trace.TokenSource = "partial"
		}
		if strings.TrimSpace(trace.UsageSourceDetail) == "" {
			if addedEstimate {
				trace.UsageSourceDetail = "prompt_and_generated_test_heuristic"
			} else {
				trace.UsageSourceDetail = "agent_usage_partial"
			}
		}
	default:
		trace.TokenSource = "missing"
		if strings.TrimSpace(trace.UsageSourceDetail) == "" {
			trace.UsageSourceDetail = "unavailable"
		}
	}

	cost, costSource := estimateCostUSD(model, trace.PromptTokens, trace.CompletionTokens, trace.TokenSource)
	trace.EstimatedCost = cost
	trace.CostSource = costSource
}

func estimateMissingTokenUsage(trace *AgentTrace, prompt string, generated string) bool {
	added := false
	if trace.PromptTokens == nil {
		if estimate := estimateTextTokenCount(prompt); estimate > 0 {
			trace.PromptTokens = intPtr(estimate)
			added = true
		}
	}
	if trace.CompletionTokens == nil {
		if estimate := estimateTextTokenCount(generated); estimate > 0 {
			trace.CompletionTokens = intPtr(estimate)
			added = true
		}
	}
	if trace.TotalTokens == nil && trace.PromptTokens != nil && trace.CompletionTokens != nil {
		trace.TotalTokens = intPtr(*trace.PromptTokens + *trace.CompletionTokens)
	}
	return added
}

func estimateTextTokenCount(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	asciiLike := 0
	cjk := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		if isCJKRune(r) {
			cjk++
			continue
		}
		asciiLike++
	}
	tokens := cjk + ceilDiv(asciiLike, 4)
	if tokens == 0 {
		return 1
	}
	return tokens
}

func isCJKRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}

func ceilDiv(n, d int) int {
	if n <= 0 {
		return 0
	}
	return (n + d - 1) / d
}

func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func estimateCostUSD(model modelConfig, promptTokens, completionTokens *int, tokenSource string) (*float64, string) {
	if promptTokens == nil || completionTokens == nil {
		return nil, "unavailable"
	}
	if model.Pricing.PromptPer1KUSD <= 0 && model.Pricing.CompletionPer1KUSD <= 0 {
		return nil, "unavailable"
	}
	cost := (float64(*promptTokens) / 1000.0 * model.Pricing.PromptPer1KUSD) +
		(float64(*completionTokens) / 1000.0 * model.Pricing.CompletionPer1KUSD)
	source := strings.ToLower(strings.TrimSpace(tokenSource))
	if source == "" {
		source = "unknown"
	}
	return floatPtr(cost), source + "_tokens+configured_pricing"
}

func extractLikelyPaths(line string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, match := range pathPattern.FindAllStringSubmatch(line, -1) {
		if len(match) != 2 {
			continue
		}
		candidate := strings.Trim(match[1], "\"'`,:;()[]{}")
		if !looksLikeFilePath(candidate) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

func extractRelevantPaths(line string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, candidate := range extractLikelyPaths(line) {
		if !isRelevantWorkspacePath(candidate) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	for _, match := range extTokenPattern.FindAllStringSubmatch(line, -1) {
		if len(match) != 2 {
			continue
		}
		candidate := strings.TrimSpace(strings.Trim(match[1], "\"'`"))
		if !isRelevantWorkspacePath(candidate) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

func isRelevantWorkspacePath(candidate string) bool {
	candidate = strings.TrimSpace(filepath.ToSlash(candidate))
	if candidate == "" {
		return false
	}
	candidate = strings.TrimPrefix(candidate, "./")
	if strings.HasPrefix(candidate, "/workspace/.utbench/") ||
		strings.HasPrefix(candidate, ".utbench/") ||
		strings.HasPrefix(candidate, "/workspace/test_runner") ||
		candidate == "test_runner" ||
		strings.HasPrefix(candidate, "/workspace/go.mod") ||
		candidate == "go.mod" {
		return false
	}
	base := filepath.Base(candidate)
	switch {
	case strings.HasPrefix(candidate, "/workspace/"):
		return true
	case base == "utbench_agent_prompt.md":
		return true
	case strings.HasPrefix(base, "generated_test."):
		return true
	default:
		return false
	}
}

func isRelevantWorkspaceWritePath(candidate string) bool {
	candidate = strings.TrimSpace(filepath.ToSlash(candidate))
	if !isRelevantWorkspacePath(candidate) {
		return false
	}
	return filepath.Base(candidate) != "utbench_agent_prompt.md"
}

func looksLikeRealCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || len(cmd) > 500 {
		return false
	}
	if strings.HasPrefix(strings.ToLower(cmd), "info ") {
		return false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 2 {
		if _, err := strconv.Atoi(fields[0]); err == nil && strings.HasPrefix(fields[1], "/tmp/") {
			return false
		}
	}
	return true
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}

// containerPath 根据沙箱模式返回路径。
// docker 模式返回容器内路径（如 /workspace/...），local 模式返回实际本地路径。
func containerPath(dockerPath, localPath, mode string) string {
	if strings.EqualFold(mode, "local") {
		return localPath
	}
	return dockerPath
}
