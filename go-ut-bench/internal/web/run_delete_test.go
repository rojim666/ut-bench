package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"
	"go-ut-bench/internal/store"
)

func TestHandleRunDeleteDeletesReadOnlyDiskAssetAndDBRows(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	outputRoot := filepath.Join(tmp, "artifacts")
	runID := "run_delete"
	runDir := filepath.Join(outputRoot, "runs", runID)
	manifestPath := filepath.Join(runDir, "generated", "generated_manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := contracts.GeneratedManifest{
		SchemaVersion:   contracts.SchemaVersion,
		RunID:           runID,
		CreatedAtUTC:    time.Now().UTC(),
		PromptStrategy:  "structured-v1",
		PromptVersionID: "prompt-v1",
		Spec: contracts.RunSpec{
			RunID:          runID,
			Models:         []string{"deepseek"},
			Languages:      []string{"python"},
			DatasetClasses: []string{"self_contained"},
			OutputRoot:     outputRoot,
		},
	}
	if err := contracts.WriteJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	lockedByAttribute := filepath.Join(runDir, "generated", "readonly.txt")
	if err := os.WriteFile(lockedByAttribute, []byte("readonly"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockedByAttribute, 0o444); err != nil {
		t.Fatal(err)
	}

	db, err := store.OpenSQLite(filepath.Join(tmp, "utbench.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.IngestManifestFile(ctx, manifestPath); err != nil {
		t.Fatal(err)
	}

	mgr := NewRunManager("", "", "", outputRoot, filepath.Join(tmp, "utbench.db"), DockerConfig{})
	defer mgr.Close()
	s := &Server{mgr: mgr, outputRoot: outputRoot, db: db}
	req := httptest.NewRequest(http.MethodDelete, "/api/runs/"+runID, nil)
	rr := httptest.NewRecorder()

	s.handleRunDelete(rr, req, runID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("expected run dir to be deleted, stat err=%v", err)
	}
	rows, err := db.ListRuns(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected DB run rows to be deleted, got %+v", rows)
	}
}

func TestResolveRunDirForDeleteRejectsTraversal(t *testing.T) {
	if _, err := resolveRunDirForDelete(t.TempDir(), ".."); err == nil {
		t.Fatal("expected traversal run id to be rejected")
	}
	if _, err := resolveRunDirForDelete(t.TempDir(), `foo\bar`); err == nil {
		t.Fatal("expected path separator run id to be rejected")
	}
}
