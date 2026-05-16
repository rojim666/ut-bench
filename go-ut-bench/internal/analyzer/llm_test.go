package analyzer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLLMJSONExtractsMarkdownWrappedJSON(t *testing.T) {
	result, err := parseLLMJSON("```json\n{\"summary\":\"ok\",\"findings\":[],\"recommendations\":[]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "ok" {
		t.Fatalf("summary = %q", result.Summary)
	}
}

func TestResolveEndpointAppendsChatCompletionsForCompatibleBase(t *testing.T) {
	cases := map[string]string{
		"https://api.example.com/v1":                        "https://api.example.com/v1/chat/completions",
		"https://api.deepseek.com":                          "https://api.deepseek.com/chat/completions",
		"https://dashscope.aliyuncs.com/compatible-mode/v1": "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
		"https://api.example.com/v1/chat/completions":       "https://api.example.com/v1/chat/completions",
	}
	for input, want := range cases {
		got := resolveEndpoint(llmModelConfig{Provider: "openai", Endpoint: input})
		if strings.Contains(input, "dashscope") {
			got = resolveEndpoint(llmModelConfig{Provider: "dashscope", Endpoint: input})
		}
		if got != want {
			t.Fatalf("resolveEndpoint(%s) = %s, want %s", input, got, want)
		}
	}
}

func TestLoadAnalysisModelAutoSelectsEnabledModelWithAPIKey(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
models:
  a:
    enabled: true
    provider: openai
    config:
      api_endpoint: https://a.example.com/v1
      model: a-model
      api_key_env: MODEL_A_KEY
  b:
    enabled: true
    provider: openai
    config:
      api_endpoint: https://b.example.com/v1
      model: b-model
      api_key_env: MODEL_B_KEY
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MODEL_B_KEY", "token")
	model, err := loadAnalysisModel(cfgPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if model.Name != "b" {
		t.Fatalf("selected model = %s", model.Name)
	}
}

func TestHTTPClientAnalyzeParsesOpenAIStyleJSON(t *testing.T) {
	var pathSeen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathSeen = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"summary\":\"ok\",\"findings\":[{\"title\":\"x\",\"evidence\":[{\"evidence_id\":\"ev-001\"}]}],\"recommendations\":[]}"}}]}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
models:
  fake:
    enabled: true
    provider: openai
    config:
      api_endpoint: %s/v1
      model: fake-model
      api_key_env: FAKE_MODEL_KEY
`, server.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MODEL_KEY", "token")
	result, err := (&HTTPClient{Client: server.Client()}).Analyze(context.Background(), LLMRequest{
		ConfigPath: cfgPath,
		ModelName:  "fake",
		Prompt:     "diagnose",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pathSeen != "/v1/chat/completions" {
		t.Fatalf("path = %s", pathSeen)
	}
	if result.Status != "ok" || result.Summary != "ok" || len(result.Findings) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestHTTPClientAnalyzeDegradesOnInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"not json"}}]}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
models:
  fake:
    enabled: true
    provider: openai
    config:
      api_endpoint: %s/v1
      model: fake-model
      api_key_env: FAKE_MODEL_KEY
`, server.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MODEL_KEY", "token")
	result, err := (&HTTPClient{Client: server.Client()}).Analyze(context.Background(), LLMRequest{
		ConfigPath: cfgPath,
		ModelName:  "fake",
		Prompt:     "diagnose",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "degraded" || result.Error == "" {
		t.Fatalf("expected degraded parse result, got %+v", result)
	}
}

func TestHTTPClientStreamTextParsesOpenAIStyleSSE(t *testing.T) {
	var pathSeen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathSeen = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
models:
  fake:
    enabled: true
    provider: openai
    config:
      api_endpoint: %s/v1
      model: fake-model
      api_key_env: FAKE_MODEL_KEY
`, server.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MODEL_KEY", "token")
	var deltas []string
	text, err := (&HTTPClient{Client: server.Client()}).StreamText(context.Background(), LLMTextRequest{
		ConfigPath: cfgPath,
		ModelName:  "fake",
		UserPrompt: "hello",
		OnDelta: func(delta string) {
			deltas = append(deltas, delta)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pathSeen != "/v1/chat/completions" {
		t.Fatalf("path = %s", pathSeen)
	}
	if text != "你好" || strings.Join(deltas, "") != "你好" {
		t.Fatalf("stream text=%q deltas=%q", text, strings.Join(deltas, ""))
	}
}

func TestBuildChatPayloadUsesAnalysisSizedMaxTokens(t *testing.T) {
	payload := buildChatPayload(llmModelConfig{
		Provider: "openai",
		Model:    "fake",
		Params: map[string]any{
			"max_tokens": 4096,
		},
	}, "diagnose")
	if payload["max_tokens"] != 8192 {
		t.Fatalf("max_tokens = %v, want 8192", payload["max_tokens"])
	}
}
