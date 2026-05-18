package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-ut-bench/internal/analyzer"
	"go-ut-bench/internal/contracts"
)

type analysisChatSessionRequest struct {
	LLMModel         string                              `json:"llm_model,omitempty"`
	SelectedSubjects []contracts.AnalysisSubjectSelector `json:"selected_subjects,omitempty"`
}

type analysisChatMessageRequest struct {
	Content          string `json:"content"`
	LLMModel         string `json:"llm_model,omitempty"`
	FocusEvidenceID  string `json:"focus_evidence_id,omitempty"`
	FocusRootCauseID string `json:"focus_root_cause_id,omitempty"`
}

func (s *Server) handleRunAnalysisChat(w http.ResponseWriter, r *http.Request, runID, suffix string) {
	suffix = strings.Trim(suffix, "/")
	switch {
	case suffix == "sessions" && r.Method == http.MethodPost:
		s.createAnalysisChatSession(w, r, runID)
	case suffix == "sessions" && r.Method == http.MethodGet:
		s.listAnalysisChatSessions(w, r, runID)
	case strings.HasPrefix(suffix, "sessions/"):
		s.handleAnalysisChatSession(w, r, runID, strings.TrimPrefix(suffix, "sessions/"))
	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAnalysisChatSession(w http.ResponseWriter, r *http.Request, runID, suffix string) {
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.getAnalysisChatSession(w, r, runID, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "messages:stream" && r.Method == http.MethodPost {
		s.streamAnalysisChatMessage(w, r, runID, parts[0])
		return
	}
	errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *Server) createAnalysisChatSession(w http.ResponseWriter, r *http.Request, runID string) {
	var req analysisChatSessionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if len(req.SelectedSubjects) > 3 {
		errJSON(w, http.StatusBadRequest, "selected_subjects supports at most 3 items")
		return
	}
	report, err := s.readAnalysisReport(runID)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "请先生成 AI 分析报告: "+err.Error())
		return
	}
	selected, err := normalizeChatSelection(report, req.SelectedSubjects)
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	session := contracts.AnalysisChatSession{
		SchemaVersion:    contracts.AnalysisChatSessionSchemaVersion,
		SessionID:        newAnalysisJobID(),
		RunID:            runID,
		CreatedAt:        now,
		UpdatedAt:        now,
		Title:            analysisChatTitle(selected),
		LLMModel:         strings.TrimSpace(req.LLMModel),
		SelectedSubjects: selected,
	}
	if err := s.writeAnalysisChatSession(&session); err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) listAnalysisChatSessions(w http.ResponseWriter, _ *http.Request, runID string) {
	dir := s.analysisChatDir(runID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []contracts.AnalysisChatSession{})
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	var sessions []contracts.AnalysisChatSession
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		session, err := s.readAnalysisChatSession(runID, strings.TrimSuffix(entry.Name(), ".json"))
		if err == nil && session.RunID == runID {
			sessions = append(sessions, *session)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) getAnalysisChatSession(w http.ResponseWriter, _ *http.Request, runID, sessionID string) {
	session, err := s.readAnalysisChatSession(runID, sessionID)
	if err != nil {
		errJSON(w, http.StatusNotFound, "analysis chat session not found")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) streamAnalysisChatMessage(w http.ResponseWriter, r *http.Request, runID, sessionID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		errJSON(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	var req analysisChatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		errJSON(w, http.StatusBadRequest, "content is required")
		return
	}
	session, err := s.readAnalysisChatSession(runID, sessionID)
	if err != nil {
		errJSON(w, http.StatusNotFound, "analysis chat session not found")
		return
	}
	report, err := s.readAnalysisReport(runID)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "请先生成 AI 分析报告: "+err.Error())
		return
	}

	now := time.Now().UTC()
	userMsg := contracts.AnalysisChatMessage{
		MessageID:        newAnalysisJobID(),
		Role:             "user",
		Content:          req.Content,
		Status:           "succeeded",
		CreatedAt:        now,
		SelectedSubjects: session.SelectedSubjects,
	}
	assistantMsg := contracts.AnalysisChatMessage{
		MessageID:        newAnalysisJobID(),
		Role:             "assistant",
		Status:           "running",
		CreatedAt:        now,
		SelectedSubjects: session.SelectedSubjects,
	}
	session.Messages = append(session.Messages, userMsg)
	session.UpdatedAt = now
	_ = s.writeAnalysisChatSession(session)

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	writeSSE(w, flusher, "message_start", map[string]any{
		"message_id":        assistantMsg.MessageID,
		"selected_subjects": session.SelectedSubjects,
	})

	start := time.Now()
	systemPrompt, userPrompt := buildAnalysisChatPrompt(report, session, req.Content, req.FocusEvidenceID, req.FocusRootCauseID)
	modelName := firstNonEmpty(strings.TrimSpace(req.LLMModel), strings.TrimSpace(session.LLMModel), strings.TrimSpace(report.LLMStatus.Model))
	var content bytes.Buffer
	text, err := (&analyzer.HTTPClient{}).StreamText(r.Context(), analyzer.LLMTextRequest{
		ConfigPath:   s.configPath,
		ModelName:    modelName,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		OnDelta: func(delta string) {
			content.WriteString(delta)
			writeSSE(w, flusher, "delta", map[string]string{"text": delta})
		},
	})
	if text != "" && content.Len() == 0 {
		content.WriteString(text)
	}
	assistantMsg.Content = content.String()
	assistantMsg.ElapsedMS = time.Since(start).Milliseconds()
	session.LLMModel = modelName
	if err != nil {
		assistantMsg.Status = "failed"
		assistantMsg.Error = err.Error()
		writeSSE(w, flusher, "error", map[string]string{"error": err.Error()})
	} else {
		assistantMsg.Status = "succeeded"
		writeSSE(w, flusher, "message_end", map[string]any{
			"message_id":  assistantMsg.MessageID,
			"elapsed_ms":  assistantMsg.ElapsedMS,
			"content_len": len(assistantMsg.Content),
		})
	}
	session.Messages = append(session.Messages, assistantMsg)
	session.UpdatedAt = time.Now().UTC()
	_ = s.writeAnalysisChatSession(session)
}

func (s *Server) readAnalysisReport(runID string) (*contracts.AnalysisReport, error) {
	path := filepath.Join(s.outputRoot, "runs", runID, "analysis", "analysis_report.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report contracts.AnalysisReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

func normalizeChatSelection(report *contracts.AnalysisReport, selected []contracts.AnalysisSubjectSelector) ([]contracts.AnalysisSubjectSelector, error) {
	if len(selected) == 0 {
		selected = report.Selection.SelectedSubjects
	}
	if len(selected) > 3 {
		return nil, fmt.Errorf("selected_subjects supports at most 3 items")
	}
	available := map[string]bool{}
	for _, subject := range report.Subjects {
		available[analysisChatSubjectKey(subject.SubjectID, subject.SampleID, subject.Language)] = true
	}
	seen := map[string]bool{}
	var out []contracts.AnalysisSubjectSelector
	for _, item := range selected {
		item = contracts.AnalysisSubjectSelector{
			SubjectID: strings.TrimSpace(item.SubjectID),
			SampleID:  strings.TrimSpace(item.SampleID),
			Language:  strings.TrimSpace(item.Language),
		}
		if item.SubjectID == "" || item.SampleID == "" || item.Language == "" {
			return nil, fmt.Errorf("selected_subjects contains empty subject_id/sample_id/language")
		}
		key := analysisChatSubjectKey(item.SubjectID, item.SampleID, item.Language)
		if !available[key] {
			return nil, fmt.Errorf("selected subject not found: subject_id=%s sample_id=%s language=%s", item.SubjectID, item.SampleID, item.Language)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out, nil
}

func buildAnalysisChatPrompt(report *contracts.AnalysisReport, session *contracts.AnalysisChatSession, question string, focusEvidenceID string, focusRootCauseID string) (string, string) {
	system := `你是 UTBench 的 Agent 行为分析助手。你只基于当前分析报告、已选对象和 trace 摘要回答；如果证据不足，要明确说明缺少什么证据。回答使用中文，尽量给出可执行判断，但不要声称已经自动修改 skill、prompt 或执行复测。`
	var b strings.Builder
	b.WriteString("当前用户问题：\n")
	b.WriteString(question)
	b.WriteString("\n\nRun 总览：\n")
	b.WriteString(fmt.Sprintf("- run_id: %s\n- headline: %s\n", report.RunID, report.Summary.Headline))
	if report.LLM != nil && strings.TrimSpace(report.LLM.Summary) != "" {
		b.WriteString("- llm_summary: " + report.LLM.Summary + "\n")
	}
	if len(report.ReportInsights) > 0 {
		b.WriteString("\n报告级洞察：\n")
		for _, insight := range limitChatReportInsights(report.ReportInsights, 8) {
			b.WriteString(fmt.Sprintf("- [%s/%s/%s] %s: %s\n", firstNonEmpty(insight.Priority, "P?"), firstNonEmpty(insight.Source, "rule"), firstNonEmpty(insight.Category, "report"), insight.Title, trim(insight.Detail, 260)))
		}
	}
	if report.EvolutionPlan != nil {
		b.WriteString("\n自进化建议：\n")
		if report.EvolutionPlan.Summary != "" {
			b.WriteString("- summary: " + trim(report.EvolutionPlan.Summary, 260) + "\n")
		}
		for _, item := range limitChatEvolutionItems(report.EvolutionPlan.Items, 5) {
			b.WriteString(fmt.Sprintf("- [%s/%s/%s] %s: %s\n", firstNonEmpty(item.Priority, "P?"), firstNonEmpty(item.Source, "rule"), firstNonEmpty(item.Target, "skill"), item.Title, trim(item.Reason, 260)))
		}
	}
	if focus := findAnalysisRootCause(report.RootCauses, focusRootCauseID); focus != nil {
		b.WriteString("\n当前聚焦根因：\n")
		b.WriteString(fmt.Sprintf("- [%s/%s] %s: %s\n", focus.Severity, focus.Category, focus.Title, trim(focus.Detail, 500)))
		if focus.RecommendedAction != "" {
			b.WriteString("- recommended_action: " + trim(focus.RecommendedAction, 300) + "\n")
		}
	}
	if evidence := findAnalysisEvidence(report.EvidenceIndex, focusEvidenceID); evidence != nil {
		b.WriteString("\n当前聚焦证据：\n")
		b.WriteString(fmt.Sprintf("- %s / %s / %s / step#%d\n", evidence.EvidenceID, evidence.Kind, evidence.Title, evidence.StepIndex))
		b.WriteString("- subject: " + evidence.SubjectID + " / " + evidence.SampleID + " / " + evidence.Language + "\n")
		b.WriteString("- excerpt: " + trim(evidence.Excerpt, 900) + "\n")
	}
	b.WriteString("\n当前选择对象：\n")
	selectedSubjects := selectedAnalysisChatSubjects(report, session.SelectedSubjects)
	if len(selectedSubjects) == 0 {
		b.WriteString("- 未手动选择对象；请基于报告摘要回答。\n")
	} else {
		for _, subject := range selectedSubjects {
			b.WriteString(renderChatSubjectContext(report, subject))
		}
	}
	b.WriteString("\n相关诊断：\n")
	for _, f := range limitChatFindings(report.Findings, session.SelectedSubjects, focusRootCauseID, report.RootCauses, 8) {
		b.WriteString(fmt.Sprintf("- [%s/%s/%s] %s: %s\n", firstNonEmpty(f.Severity, "P?"), firstNonEmpty(f.Source, "rule"), firstNonEmpty(f.Category, "unknown"), f.Title, trim(f.Detail, 220)))
	}
	b.WriteString("\n相关建议：\n")
	for _, rec := range limitChatRecommendations(report.Recommendations, session.SelectedSubjects, 8) {
		b.WriteString(fmt.Sprintf("- [%s/%s/%s] %s: %s\n", firstNonEmpty(rec.Priority, "P?"), firstNonEmpty(rec.Source, "rule"), firstNonEmpty(rec.Target, rec.Category), rec.Title, trim(rec.Detail, 220)))
	}
	if len(session.Messages) > 0 {
		b.WriteString("\n最近追问历史：\n")
		for _, msg := range tailChatMessages(session.Messages, 8) {
			b.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, trim(msg.Content, 500)))
		}
	}
	b.WriteString("\n回答要求：\n- 先直接回答结论，再给证据和改进建议。\n- 对比问题要明确指出共同点、差异点、最好/最差原因。\n- 引用对象时使用 subject_id / sample_id / language。\n")
	return system, b.String()
}

func limitChatReportInsights(items []contracts.ReportInsight, max int) []contracts.ReportInsight {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

func limitChatEvolutionItems(items []contracts.EvolutionItem, max int) []contracts.EvolutionItem {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

func renderChatSubjectContext(report *contracts.AnalysisReport, subject contracts.AnalysisSubject) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- %s / %s / %s\n", subject.SubjectID, subject.SampleID, subject.Language))
	b.WriteString(fmt.Sprintf("  编译: %t；测试: %s；覆盖率: %s；变异: %s；trace steps: %d\n", subject.CompilePass, boolPtrLabel(subject.TestPass), metricLabel(subject.LineCoverage), metricLabel(subject.MutationScore), subject.TraceStepCount))
	b.WriteString(fmt.Sprintf("  行为: 读源码=%t 写测试=%t 运行测试=%t 修改源码=%t 策略命令=%d 噪声=%d\n", subject.HasSourceRead, subject.HasTestWrite, subject.HasTestExecution, subject.ModifiedSource, subject.PolicyCommandCount, subject.RuntimeNoiseCount))
	steps := selectedChatSteps(subject.Trajectory, 8)
	for _, step := range steps {
		excerpt := trim(strings.TrimSpace(strings.Join(nonEmptyStrings(step.TextExcerpt, step.InputExcerpt, step.OutputExcerpt), " ")), 260)
		b.WriteString(fmt.Sprintf("  step#%d %s/%s: %s\n", step.Index, step.Kind, firstNonEmpty(step.Tool, step.Role), excerpt))
	}
	_ = report
	return b.String()
}

func selectedAnalysisChatSubjects(report *contracts.AnalysisReport, selected []contracts.AnalysisSubjectSelector) []contracts.AnalysisSubject {
	if len(selected) == 0 {
		return nil
	}
	selectedKeys := map[string]bool{}
	for _, item := range selected {
		selectedKeys[analysisChatSubjectKey(item.SubjectID, item.SampleID, item.Language)] = true
	}
	var out []contracts.AnalysisSubject
	for _, subject := range report.Subjects {
		if selectedKeys[analysisChatSubjectKey(subject.SubjectID, subject.SampleID, subject.Language)] {
			out = append(out, subject)
		}
	}
	return out
}

func limitChatFindings(items []contracts.AnalysisFinding, selected []contracts.AnalysisSubjectSelector, focusRootCauseID string, rootCauses []contracts.AnalysisRootCause, max int) []contracts.AnalysisFinding {
	focused := map[string]bool{}
	if focus := findAnalysisRootCause(rootCauses, focusRootCauseID); focus != nil {
		for _, id := range focus.RelatedFindings {
			focused[id] = true
		}
	}
	var out []contracts.AnalysisFinding
	for _, item := range items {
		if len(focused) > 0 && !focused[item.ID] {
			continue
		}
		if chatAppliesToSelection(item.SubjectID, item.SampleID, selected) {
			out = append(out, item)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}

func findAnalysisRootCause(items []contracts.AnalysisRootCause, id string) *contracts.AnalysisRootCause {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func findAnalysisEvidence(items []contracts.AnalysisEvidenceItem, id string) *contracts.AnalysisEvidenceItem {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	for i := range items {
		if items[i].EvidenceID == id {
			return &items[i]
		}
	}
	return nil
}

func limitChatRecommendations(items []contracts.AnalysisRecommendation, selected []contracts.AnalysisSubjectSelector, max int) []contracts.AnalysisRecommendation {
	selectedSubjects := map[string]bool{}
	for _, item := range selected {
		selectedSubjects[item.SubjectID] = true
	}
	var out []contracts.AnalysisRecommendation
	for _, item := range items {
		if len(selectedSubjects) == 0 || len(item.AppliesTo) == 0 || anyStringInSet(item.AppliesTo, selectedSubjects) {
			out = append(out, item)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}

func chatAppliesToSelection(subjectID, sampleID string, selected []contracts.AnalysisSubjectSelector) bool {
	if len(selected) == 0 || strings.TrimSpace(subjectID) == "" {
		return true
	}
	for _, item := range selected {
		if item.SubjectID == subjectID && (sampleID == "" || item.SampleID == sampleID) {
			return true
		}
	}
	return false
}

func selectedChatSteps(steps []contracts.AnalysisTrajectoryStep, max int) []contracts.AnalysisTrajectoryStep {
	var out []contracts.AnalysisTrajectoryStep
	for _, step := range steps {
		text := strings.ToLower(step.Kind + " " + step.Tool + " " + step.TextExcerpt + " " + step.InputExcerpt + " " + step.OutputExcerpt)
		if len(out) < max && (containsAny(text, []string{"read", "write", "edit", "test", "pytest", "go test", "mvn", "ctest", "error", "failed", "policy"}) || len(out) < 3) {
			out = append(out, step)
		}
		if len(out) >= max {
			break
		}
	}
	return out
}

func tailChatMessages(items []contracts.AnalysisChatMessage, max int) []contracts.AnalysisChatMessage {
	if len(items) <= max {
		return items
	}
	return items[len(items)-max:]
}

func (s *Server) analysisChatDir(runID string) string {
	return filepath.Join(s.outputRoot, "runs", runID, "analysis", "chat_sessions")
}

func (s *Server) analysisChatPath(runID, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || filepath.Base(sessionID) != sessionID || strings.Contains(sessionID, "..") {
		return "", fmt.Errorf("invalid session id")
	}
	return filepath.Join(s.analysisChatDir(runID), sessionID+".json"), nil
}

func (s *Server) readAnalysisChatSession(runID, sessionID string) (*contracts.AnalysisChatSession, error) {
	path, err := s.analysisChatPath(runID, sessionID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var session contracts.AnalysisChatSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *Server) writeAnalysisChatSession(session *contracts.AnalysisChatSession) error {
	if session.SchemaVersion == "" {
		session.SchemaVersion = contracts.AnalysisChatSessionSchemaVersion
	}
	path, err := s.analysisChatPath(session.RunID, session.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return contracts.WriteJSON(path, session)
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		raw = []byte(`{"error":"marshal sse event failed"}`)
	}
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	flusher.Flush()
}

func analysisChatTitle(selected []contracts.AnalysisSubjectSelector) string {
	if len(selected) == 0 {
		return "全局追问"
	}
	labels := make([]string, 0, len(selected))
	for _, item := range selected {
		labels = append(labels, item.SubjectID+"/"+item.SampleID)
	}
	return strings.Join(labels, " vs ")
}

func analysisChatSubjectKey(subjectID, sampleID, language string) string {
	return strings.Join([]string{strings.TrimSpace(subjectID), strings.TrimSpace(sampleID), strings.TrimSpace(language)}, "\x00")
}

func boolPtrLabel(v *bool) string {
	if v == nil {
		return "未执行"
	}
	if *v {
		return "通过"
	}
	return "失败"
}

func metricLabel(v *float64) string {
	if v == nil {
		return "无"
	}
	n := *v
	if n <= 1 {
		n *= 100
	}
	return fmt.Sprintf("%.1f%%", n)
}

func anyStringInSet(items []string, set map[string]bool) bool {
	for _, item := range items {
		if set[item] {
			return true
		}
	}
	return false
}

func nonEmptyStrings(items ...string) []string {
	var out []string
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, item)
		}
	}
	return out
}

func firstNonEmpty(items ...string) string {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return item
		}
	}
	return ""
}

func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func trim(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
