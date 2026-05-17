package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"go-ut-bench/internal/contracts"

	"gopkg.in/yaml.v3"
)

type HTTPClient struct {
	Client *http.Client
}

type LLMTextRequest struct {
	ConfigPath   string
	ModelName    string
	SystemPrompt string
	UserPrompt   string
	OnDelta      func(string)
}

type llmModelConfig struct {
	Name      string
	Provider  string
	Endpoint  string
	Model     string
	APIKeyEnv string
	Params    map[string]any
}

type llmModelsFile struct {
	Models map[string]struct {
		Enabled  bool   `yaml:"enabled"`
		Provider string `yaml:"provider"`
		Config   struct {
			APIEndpoint string         `yaml:"api_endpoint"`
			Model       string         `yaml:"model"`
			APIKeyEnv   string         `yaml:"api_key_env"`
			Parameters  map[string]any `yaml:"parameters"`
		} `yaml:"config"`
	} `yaml:"models"`
}

func (c *HTTPClient) Analyze(ctx context.Context, req LLMRequest) (contracts.LLMAnalysisResult, error) {
	model, err := loadAnalysisModel(req.ConfigPath, req.ModelName)
	if err != nil {
		return contracts.LLMAnalysisResult{}, err
	}
	apiKey := strings.TrimSpace(os.Getenv(model.APIKeyEnv))
	if apiKey == "" {
		return contracts.LLMAnalysisResult{}, fmt.Errorf("missing API key env var: %s", model.APIKeyEnv)
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}

	payload := buildChatPayload(model, req.Prompt)
	body, err := json.Marshal(payload)
	if err != nil {
		return contracts.LLMAnalysisResult{}, err
	}
	endpoint := resolveEndpoint(model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return contracts.LLMAnalysisResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.EqualFold(model.Provider, "dashscope") && !strings.Contains(endpoint, "compatible-mode") {
		httpReq.Header.Set("X-DashScope-SSE", "disable")
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return contracts.LLMAnalysisResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return contracts.LLMAnalysisResult{}, fmt.Errorf("llm http %d: %s", resp.StatusCode, trim(string(raw), 500))
	}
	text, err := extractLLMText(raw)
	if err != nil {
		return contracts.LLMAnalysisResult{
			Model:     model.Name,
			Status:    "degraded",
			RawOutput: string(raw),
			Error:     err.Error(),
		}, nil
	}
	parsed, err := parseLLMJSON(text)
	if err != nil {
		return contracts.LLMAnalysisResult{
			Model:     model.Name,
			Status:    "degraded",
			RawOutput: text,
			Error:     err.Error(),
		}, nil
	}
	parsed.Model = model.Name
	parsed.Status = "ok"
	parsed.RawOutput = text
	return parsed, nil
}

func (c *HTTPClient) StreamText(ctx context.Context, req LLMTextRequest) (string, error) {
	model, err := loadAnalysisModel(req.ConfigPath, req.ModelName)
	if err != nil {
		return "", err
	}
	apiKey := strings.TrimSpace(os.Getenv(model.APIKeyEnv))
	if apiKey == "" {
		return "", fmt.Errorf("missing API key env var: %s", model.APIKeyEnv)
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 180 * time.Second}
	}

	payload := buildChatTextPayload(model, req.SystemPrompt, req.UserPrompt, true)
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	endpoint := resolveEndpoint(model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.EqualFold(model.Provider, "dashscope") && !strings.Contains(endpoint, "compatible-mode") {
		httpReq.Header.Set("X-DashScope-SSE", "enable")
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("llm http %d: %s", resp.StatusCode, trim(string(raw), 500))
	}

	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "event-stream") {
		raw, _ := io.ReadAll(resp.Body)
		text, err := extractLLMText(raw)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) != "" && req.OnDelta != nil {
			req.OnDelta(text)
		}
		return text, nil
	}

	var b strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		delta, done, err := extractLLMStreamDelta([]byte(data))
		if err != nil {
			return b.String(), err
		}
		if delta != "" {
			b.WriteString(delta)
			if req.OnDelta != nil {
				req.OnDelta(delta)
			}
		}
		if done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return b.String(), err
	}
	return b.String(), nil
}

func loadAnalysisModel(configPath, selected string) (llmModelConfig, error) {
	if strings.TrimSpace(configPath) == "" {
		configPath = "./configs/models.yaml"
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return llmModelConfig{}, err
	}
	var cfg llmModelsFile
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return llmModelConfig{}, err
	}
	if len(cfg.Models) == 0 {
		return llmModelConfig{}, fmt.Errorf("models config is empty")
	}
	names := make([]string, 0, len(cfg.Models))
	if strings.TrimSpace(selected) != "" {
		names = append(names, strings.TrimSpace(selected))
	} else {
		for name := range cfg.Models {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	var missingKey []string
	for _, name := range names {
		item, ok := cfg.Models[name]
		if !ok {
			if strings.TrimSpace(selected) != "" {
				return llmModelConfig{}, fmt.Errorf("model %s not found", selected)
			}
			continue
		}
		if !item.Enabled {
			if strings.TrimSpace(selected) != "" {
				return llmModelConfig{}, fmt.Errorf("model %s is disabled", selected)
			}
			continue
		}
		model := llmModelConfig{
			Name:      name,
			Provider:  strings.ToLower(strings.TrimSpace(item.Provider)),
			Endpoint:  strings.TrimRight(strings.TrimSpace(item.Config.APIEndpoint), "/"),
			Model:     strings.TrimSpace(item.Config.Model),
			APIKeyEnv: strings.TrimSpace(item.Config.APIKeyEnv),
			Params:    item.Config.Parameters,
		}
		if strings.TrimSpace(selected) != "" {
			return model, nil
		}
		if model.APIKeyEnv == "" || strings.TrimSpace(os.Getenv(model.APIKeyEnv)) == "" {
			missingKey = append(missingKey, name)
			continue
		}
		return model, nil
	}
	if strings.TrimSpace(selected) == "" && len(missingKey) > 0 {
		return llmModelConfig{}, fmt.Errorf("no enabled model with API key available")
	}
	return llmModelConfig{}, fmt.Errorf("no enabled model available")
}

func buildChatPayload(model llmModelConfig, prompt string) map[string]any {
	maxTokens := 8192
	if v, ok := intParam(model.Params["max_tokens"]); ok && v > maxTokens {
		maxTokens = v
	}
	if maxTokens > 12000 {
		maxTokens = 12000
	}
	temperature := 0.2
	if v, ok := floatParam(model.Params["temperature"]); ok {
		temperature = v
	}
	topP := 0.9
	if v, ok := floatParam(model.Params["top_p"]); ok {
		topP = v
	}
	if strings.EqualFold(model.Provider, "dashscope") && !strings.Contains(strings.ToLower(model.Endpoint), "compatible-mode") {
		return map[string]any{
			"model": model.Model,
			"input": map[string]any{
				"messages": []map[string]string{
					{"role": "user", "content": prompt},
				},
			},
			"parameters": map[string]any{
				"temperature": temperature,
				"top_p":       topP,
				"max_tokens":  maxTokens,
			},
		}
	}
	return map[string]any{
		"model": model.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "你只输出合法 JSON，不输出 markdown、解释或代码块。"},
			{"role": "user", "content": prompt},
		},
		"temperature": temperature,
		"top_p":       topP,
		"max_tokens":  maxTokens,
	}
}

func buildChatTextPayload(model llmModelConfig, systemPrompt, userPrompt string, stream bool) map[string]any {
	maxTokens := 4096
	if v, ok := intParam(model.Params["max_tokens"]); ok && v > maxTokens {
		maxTokens = v
	}
	if maxTokens > 12000 {
		maxTokens = 12000
	}
	temperature := 0.2
	if v, ok := floatParam(model.Params["temperature"]); ok {
		temperature = v
	}
	topP := 0.9
	if v, ok := floatParam(model.Params["top_p"]); ok {
		topP = v
	}
	messages := []map[string]string{}
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, map[string]string{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": userPrompt})
	if strings.EqualFold(model.Provider, "dashscope") && !strings.Contains(strings.ToLower(model.Endpoint), "compatible-mode") {
		params := map[string]any{
			"temperature": temperature,
			"top_p":       topP,
			"max_tokens":  maxTokens,
		}
		if stream {
			params["incremental_output"] = true
			params["result_format"] = "message"
		}
		return map[string]any{
			"model": model.Model,
			"input": map[string]any{
				"messages": messages,
			},
			"parameters": params,
		}
	}
	payload := map[string]any{
		"model":       model.Model,
		"messages":    messages,
		"temperature": temperature,
		"top_p":       topP,
		"max_tokens":  maxTokens,
	}
	if stream {
		payload["stream"] = true
	}
	return payload
}

func resolveEndpoint(model llmModelConfig) string {
	endpoint := strings.TrimRight(strings.TrimSpace(model.Endpoint), "/")
	lower := strings.ToLower(endpoint)
	if endpoint == "" {
		if strings.EqualFold(model.Provider, "dashscope") {
			return "https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation"
		}
		return "https://api.openai.com/v1/chat/completions"
	}
	if strings.Contains(lower, "/chat/completions") {
		return endpoint
	}
	if strings.EqualFold(model.Provider, "dashscope") && !strings.Contains(lower, "compatible-mode") {
		return endpoint
	}
	return endpoint + "/chat/completions"
}

func extractLLMText(raw []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
		if ch, ok := choices[0].(map[string]any); ok {
			if msg, ok := ch["message"].(map[string]any); ok {
				if s, ok := msg["content"].(string); ok {
					return s, nil
				}
				if parts, ok := msg["content"].([]any); ok {
					return contentPartsText(parts), nil
				}
			}
			if s, ok := ch["text"].(string); ok {
				return s, nil
			}
		}
	}
	if output, ok := payload["output"].(map[string]any); ok {
		if s, ok := output["text"].(string); ok {
			return s, nil
		}
		if choices, ok := output["choices"].([]any); ok && len(choices) > 0 {
			if ch, ok := choices[0].(map[string]any); ok {
				if msg, ok := ch["message"].(map[string]any); ok {
					if s, ok := msg["content"].(string); ok {
						return s, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("unable to extract text from llm response")
}

func extractLLMStreamDelta(raw []byte) (string, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", false, err
	}
	if errObj, ok := payload["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
			return "", false, fmt.Errorf("%s", msg)
		}
		return "", false, fmt.Errorf("llm stream error")
	}
	if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
		if ch, ok := choices[0].(map[string]any); ok {
			if delta, ok := ch["delta"].(map[string]any); ok {
				if s, ok := delta["content"].(string); ok {
					return s, streamChoiceDone(ch), nil
				}
				if parts, ok := delta["content"].([]any); ok {
					return contentPartsText(parts), streamChoiceDone(ch), nil
				}
			}
			if msg, ok := ch["message"].(map[string]any); ok {
				if s, ok := msg["content"].(string); ok {
					return s, streamChoiceDone(ch), nil
				}
			}
			if s, ok := ch["text"].(string); ok {
				return s, streamChoiceDone(ch), nil
			}
			return "", streamChoiceDone(ch), nil
		}
	}
	if output, ok := payload["output"].(map[string]any); ok {
		if s, ok := output["text"].(string); ok {
			return s, streamOutputDone(output), nil
		}
		if choices, ok := output["choices"].([]any); ok && len(choices) > 0 {
			if ch, ok := choices[0].(map[string]any); ok {
				if msg, ok := ch["message"].(map[string]any); ok {
					if s, ok := msg["content"].(string); ok {
						return s, streamChoiceDone(ch) || streamOutputDone(output), nil
					}
				}
				if delta, ok := ch["delta"].(map[string]any); ok {
					if s, ok := delta["content"].(string); ok {
						return s, streamChoiceDone(ch) || streamOutputDone(output), nil
					}
				}
				return "", streamChoiceDone(ch) || streamOutputDone(output), nil
			}
		}
		return "", streamOutputDone(output), nil
	}
	return "", false, nil
}

func streamChoiceDone(ch map[string]any) bool {
	if reason, ok := ch["finish_reason"].(string); ok && reason != "" && reason != "null" {
		return true
	}
	return false
}

func streamOutputDone(output map[string]any) bool {
	if reason, ok := output["finish_reason"].(string); ok && reason != "" && reason != "null" {
		return true
	}
	if status, ok := output["status"].(string); ok && strings.EqualFold(status, "finished") {
		return true
	}
	return false
}

func contentPartsText(parts []any) string {
	var out []string
	for _, part := range parts {
		switch p := part.(type) {
		case string:
			out = append(out, p)
		case map[string]any:
			if s, ok := p["text"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, "\n")
}

func parseLLMJSON(text string) (contracts.LLMAnalysisResult, error) {
	clean := strings.TrimSpace(text)
	if strings.HasPrefix(clean, "```") {
		clean = strings.TrimPrefix(clean, "```json")
		clean = strings.TrimPrefix(clean, "```JSON")
		clean = strings.TrimPrefix(clean, "```")
		clean = strings.TrimSuffix(clean, "```")
		clean = strings.TrimSpace(clean)
	}
	if !strings.HasPrefix(clean, "{") {
		start := strings.Index(clean, "{")
		end := strings.LastIndex(clean, "}")
		if start >= 0 && end > start {
			clean = clean[start : end+1]
		}
	}
	var wire struct {
		Summary         string                             `json:"summary"`
		Findings        []contracts.AnalysisFinding        `json:"findings"`
		Recommendations []contracts.AnalysisRecommendation `json:"recommendations"`
		ReportInsights  []contracts.ReportInsight          `json:"report_insights"`
		EvolutionPlan   *contracts.EvolutionPlan           `json:"evolution_plan"`
	}
	if err := json.Unmarshal([]byte(clean), &wire); err != nil {
		return contracts.LLMAnalysisResult{}, err
	}
	return contracts.LLMAnalysisResult{
		Summary:         wire.Summary,
		Findings:        wire.Findings,
		Recommendations: wire.Recommendations,
		ReportInsights:  wire.ReportInsights,
		EvolutionPlan:   wire.EvolutionPlan,
	}, nil
}

func intParam(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	default:
		return 0, false
	}
}

func floatParam(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
