package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateModelCanRenameModelKey(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(configPath, []byte(`
models:
  old-model:
    enabled: true
    provider: deepseek
    custom_field: keep-me
    config:
      model: old-id
      api_endpoint: https://old.example/v1
      api_key_env: OLD_MODEL_API_KEY
      extra_config: keep-config
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{configPath: configPath}
	body := `{
		"name":"new-model",
		"enabled":false,
		"provider":"openai",
		"model_id":"new-id",
		"api_endpoint":"https://new.example/v1",
		"api_key_env":"NEW_MODEL_API_KEY",
		"parameters":{"temperature":0.2}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/models/old-model", strings.NewReader(body))
	rec := httptest.NewRecorder()

	s.updateModel(rec, req, "old-model")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	root, err := readModelsRoot(configPath)
	if err != nil {
		t.Fatal(err)
	}
	models := ensureModelsMap(root)
	if _, exists := models["old-model"]; exists {
		t.Fatalf("old model key still exists: %#v", models["old-model"])
	}
	node, exists := models["new-model"].(map[string]any)
	if !exists {
		t.Fatalf("new model key missing: %#v", models)
	}
	if node["custom_field"] != "keep-me" {
		t.Fatalf("custom field was not preserved: %#v", node)
	}
	cfg, _ := node["config"].(map[string]any)
	if cfg["model"] != "new-id" || cfg["api_endpoint"] != "https://new.example/v1" || cfg["extra_config"] != "keep-config" {
		t.Fatalf("config not updated/preserved: %#v", cfg)
	}
}

func TestUpdateModelRenameRejectsExistingName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(configPath, []byte(`
models:
  old-model:
    enabled: true
    provider: deepseek
    config:
      model: old-id
      api_endpoint: https://old.example/v1
      api_key_env: OLD_MODEL_API_KEY
  existing-model:
    enabled: true
    provider: openai
    config:
      model: existing-id
      api_endpoint: https://existing.example/v1
      api_key_env: EXISTING_MODEL_API_KEY
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{configPath: configPath}
	body := `{"name":"existing-model","enabled":true,"provider":"deepseek","model_id":"new-id","api_endpoint":"https://new.example/v1","api_key_env":"NEW_MODEL_API_KEY"}`
	req := httptest.NewRequest(http.MethodPut, "/api/models/old-model", strings.NewReader(body))
	rec := httptest.NewRecorder()

	s.updateModel(rec, req, "old-model")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	root, err := readModelsRoot(configPath)
	if err != nil {
		t.Fatal(err)
	}
	models := ensureModelsMap(root)
	if _, exists := models["old-model"]; !exists {
		t.Fatalf("old model key should remain after conflict")
	}
}
