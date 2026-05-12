package runner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const trajectorySchemaVersion = "trajectory.v0.1.0"

var toolExitCodePattern = regexp.MustCompile(`(?i)(?:exit code|exit status)\s*:?\s*(-?\d+)`)

func writeRawAgentOutputs(rawTracePath, rawStdoutPath, rawStderrPath, stdout, stderr string) error {
	if err := os.MkdirAll(filepath.Dir(rawTracePath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(rawStdoutPath, []byte(stdout), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(rawStderrPath, []byte(stderr), 0o644); err != nil {
		return err
	}
	f, err := os.Create(rawTracePath)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	writeStreamLines := func(stream, value string) {
		scanner := bufio.NewScanner(strings.NewReader(value))
		buf := make([]byte, 0, 1024*1024)
		scanner.Buffer(buf, 16*1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			_ = enc.Encode(map[string]any{
				"stream": stream,
				"line_no": lineNo,
				"line":   scanner.Text(),
			})
		}
	}
	writeStreamLines("stdout", stdout)
	writeStreamLines("stderr", stderr)
	return nil
}

func writeAgentTrajectory(path string, trace AgentTrace, generatedTestPath, errorText, stdout, stderr string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	traj := buildAgentTrajectory(trace, generatedTestPath, errorText, stdout, stderr)
	return contractsWriteJSON(path, traj)
}

func buildAgentTrajectory(trace AgentTrace, generatedTestPath, errorText, stdout, stderr string) AgentTrajectory {
	steps := make([]TrajectoryStep, 0, len(trace.ToolCalls)+len(trace.CommandsExecuted)+4)
	if stdout == "" {
		stdout = trace.Stdout
	}
	if stderr == "" {
		stderr = trace.Stderr
	}
	switch {
	case strings.EqualFold(trace.Framework, "codebuddy") ||
		strings.EqualFold(trace.Framework, "claudecode") ||
		strings.EqualFold(trace.Framework, "claude_code") ||
		strings.EqualFold(trace.Framework, "claude-code"):
		steps = append(steps, parseStreamJSONTrajectory(stdout, "stdout")...)
		steps = append(steps, parseStreamJSONTrajectory(stderr, "stderr")...)
	case strings.EqualFold(trace.Framework, "opencode"):
		steps = append(steps, parseOpenCodeSessionTrajectory(trace.SessionExportPath)...)
		if len(steps) == 0 {
			steps = append(steps, parseTextLogTrajectory(stdout, "stdout")...)
			steps = append(steps, parseTextLogTrajectory(stderr, "stderr")...)
		}
	default:
		steps = append(steps, parseTextLogTrajectory(stdout, "stdout")...)
		steps = append(steps, parseTextLogTrajectory(stderr, "stderr")...)
	}
	if len(steps) == 0 {
		for _, call := range trace.ToolCalls {
			steps = append(steps, TrajectoryStep{
				Kind:    "tool_call",
				Tool:    call.Tool,
				Input:   call.Input,
				Output:  call.Output,
				Success: boolPtr(call.Success),
				Source:  "parsed_log",
			})
		}
	}
	for _, cmd := range trace.CommandsExecuted {
		steps = append(steps, TrajectoryStep{Kind: "command", Tool: "bash", Input: map[string]any{"command": cmd}, Source: "parsed_log"})
	}
	for i := range steps {
		steps[i].Index = i + 1
	}
	return AgentTrajectory{
		SchemaVersion:     trajectorySchemaVersion,
		SubjectID:         trace.SubjectID,
		Framework:         trace.Framework,
		Model:             trace.Model,
		Skill:             trace.Skill,
		SampleID:          trace.SampleID,
		Language:          trace.Language,
		SessionID:         trace.SessionID,
		StartedAt:         trace.StartedAt,
		FinishedAt:        trace.FinishedAt,
		RawTracePath:      trace.RawTracePath,
		RawStdoutPath:     trace.RawStdoutPath,
		RawStderrPath:     trace.RawStderrPath,
		SessionExportPath: trace.SessionExportPath,
		Steps:             steps,
		Outcome: TrajectoryOutcome{
			ExitCode:          trace.ExitCode,
			DurationMS:        trace.DurationMS,
			GeneratedTestPath: generatedTestPath,
			WorkspaceDiff:     trace.WorkspaceDiff,
			Error:             errorText,
		},
	}
}

func parseStreamJSONTrajectory(output, source string) []TrajectoryStep {
	var steps []TrajectoryStep
	scanner := bufio.NewScanner(strings.NewReader(output))
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		steps = append(steps, trajectoryStepsFromEvent(event, source)...)
	}
	return steps
}

func parseOpenCodeSessionTrajectory(path string) []TrajectoryStep {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	var steps []TrajectoryStep
	for _, msg := range findMessageObjects(payload) {
		steps = append(steps, trajectoryStepsFromEvent(msg, "session_export")...)
	}
	return steps
}

func parseTextLogTrajectory(output, source string) []TrajectoryStep {
	var steps []TrajectoryStep
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if match := bashPattern.FindStringSubmatch(line); len(match) == 2 {
			steps = append(steps, TrajectoryStep{Kind: "command", Tool: "bash", Input: map[string]any{"command": strings.TrimSpace(match[1])}, Source: source})
			continue
		}
		if match := toolStartPattern.FindStringSubmatch(line); len(match) == 2 {
			steps = append(steps, TrajectoryStep{Kind: "tool_call", Tool: strings.TrimSpace(match[1]), Text: trimText(line, 1000), Source: source})
		}
	}
	return steps
}

func trajectoryStepsFromEvent(event map[string]any, source string) []TrajectoryStep {
	var steps []TrajectoryStep
	rawType, _ := event["type"].(string)
	role := firstString(event, "role")
	msg, _ := event["message"].(map[string]any)
	if msg != nil {
		if role == "" {
			role = firstString(msg, "role")
		}
		if rawType == "" {
			rawType = firstString(msg, "type")
		}
	}
	if text := eventText(event); text != "" {
		steps = append(steps, TrajectoryStep{Kind: "message", Role: role, Text: trimText(text, 4000), Source: source, RawType: rawType, RawEvent: compactRawEvent(event)})
	}
	for _, block := range contentBlocks(event) {
		blockType := firstString(block, "type")
		switch blockType {
		case "thinking":
			if text := firstString(block, "thinking", "text"); text != "" {
				steps = append(steps, TrajectoryStep{
					Kind:     "thinking",
					Role:     role,
					Text:     trimText(text, 4000),
					Source:   source,
					RawType:  blockType,
					RawEvent: compactRawEvent(event),
				})
			}
		case "tool_use", "function_call":
			steps = append(steps, TrajectoryStep{
				Kind:       "tool_call",
				Role:       role,
				Tool:       firstString(block, "name"),
				ToolCallID: firstString(block, "id", "tool_use_id", "tool_call_id"),
				Input:      compactAny(firstAny(block, "input", "arguments", "args"), 3, 4000),
				Source:     source,
				RawType:    blockType,
				RawEvent:   compactRawEvent(event),
			})
		case "tool_result", "function_result":
			output := compactAny(firstAny(block, "content", "output", "result"), 3, 4000)
			success := successFromBlock(block)
			exitCode := exitCodeFromValue(output)
			if exitCode != nil && *exitCode != 0 {
				success = boolPtr(false)
			}
			steps = append(steps, TrajectoryStep{
				Kind:       "tool_result",
				Role:       role,
				ToolCallID: firstString(block, "tool_use_id", "tool_call_id", "id"),
				Output:     output,
				Success:    success,
				ExitCode:   exitCode,
				Source:     source,
				RawType:    blockType,
				RawEvent:   compactRawEvent(event),
			})
		case "text":
			if text := firstString(block, "text"); text != "" {
				steps = append(steps, TrajectoryStep{Kind: "message", Role: role, Text: trimText(text, 4000), Source: source, RawType: blockType, RawEvent: compactRawEvent(event)})
			}
		}
	}
	if len(steps) == 0 && (rawType != "" || len(event) > 0) {
		steps = append(steps, TrajectoryStep{Kind: "raw_event", Role: role, Source: source, RawType: rawType, RawEvent: compactRawEvent(event)})
	}
	return steps
}

func contentBlocks(event map[string]any) []map[string]any {
	var out []map[string]any
	collectBlocks := func(value any) {
		if blocks, ok := value.([]any); ok {
			for _, block := range blocks {
				if m, ok := block.(map[string]any); ok {
					out = append(out, m)
				}
			}
		}
	}
	collectBlocks(event["content"])
	if msg, ok := event["message"].(map[string]any); ok {
		collectBlocks(msg["content"])
	}
	return out
}

func findMessageObjects(value any) []map[string]any {
	var out []map[string]any
	switch v := value.(type) {
	case map[string]any:
		if _, hasRole := v["role"]; hasRole {
			out = append(out, v)
		} else if _, hasContent := v["content"]; hasContent {
			out = append(out, v)
		}
		for _, child := range v {
			out = append(out, findMessageObjects(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, findMessageObjects(child)...)
		}
	}
	return out
}

func eventText(event map[string]any) string {
	if text := firstString(event, "text", "content_text", "result"); text != "" {
		return text
	}
	if content, ok := event["content"].(string); ok {
		return content
	}
	if msg, ok := event["message"].(map[string]any); ok {
		if text := firstString(msg, "text", "content_text", "result"); text != "" {
			return text
		}
		if content, ok := msg["content"].(string); ok {
			return content
		}
	}
	return ""
}

func successFromBlock(block map[string]any) *bool {
	if isErr, ok := block["is_error"].(bool); ok {
		return boolPtr(!isErr)
	}
	if success, ok := block["success"].(bool); ok {
		return boolPtr(success)
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstAny(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			return value
		}
	}
	return nil
}

func compactRawEvent(event map[string]any) map[string]any {
	raw := make(map[string]any, len(event))
	for key, value := range event {
		raw[key] = compactAny(value, 3, 1200)
	}
	return raw
}

func compactAny(value any, depth int, maxString int) any {
	if depth <= 0 {
		switch v := value.(type) {
		case string:
			return trimText(v, maxString)
		case nil, bool, float64, int, int64, json.Number:
			return v
		default:
			return "[truncated]"
		}
	}
	switch v := value.(type) {
	case string:
		return trimText(v, maxString)
	case []any:
		limit := len(v)
		if limit > 12 {
			limit = 12
		}
		out := make([]any, 0, limit+1)
		for i := 0; i < limit; i++ {
			out = append(out, compactAny(v[i], depth-1, maxString))
		}
		if len(v) > limit {
			out = append(out, map[string]any{"truncated_items": len(v) - limit})
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		count := 0
		for key, child := range v {
			if count >= 40 {
				out["_truncated_keys"] = len(v) - count
				break
			}
			out[key] = compactAny(child, depth-1, maxString)
			count++
		}
		return out
	default:
		return v
	}
}

func exitCodeFromValue(value any) *int {
	text := strings.TrimSpace(textFromAny(value, 0))
	if text == "" {
		return nil
	}
	match := toolExitCodePattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return nil
	}
	code, err := strconv.Atoi(match[1])
	if err != nil {
		return nil
	}
	return &code
}

func textFromAny(value any, depth int) string {
	if depth > 4 || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := textFromAny(item, depth+1); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "content", "output", "result"} {
			if text := textFromAny(v[key], depth+1); text != "" {
				return text
			}
		}
	}
	return ""
}

func boolPtr(value bool) *bool {
	return &value
}

func contractsWriteJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}
