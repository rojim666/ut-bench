package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type runDeleteResponse struct {
	Status        string   `json:"status"`
	RunID         string   `json:"run_id"`
	DeletePending bool     `json:"delete_pending,omitempty"`
	TrashPath     string   `json:"trash_path,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

type runDirectoryDeleteOutcome struct {
	Status        string
	DeletePending bool
	TrashPath     string
}

func parseForceDelete(r *http.Request) bool {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("force")))
	return raw == "1" || raw == "true" || raw == "yes"
}

func (s *Server) deleteRunAssets(ctx context.Context, runID string, force bool) (runDeleteResponse, int, error) {
	runID = strings.TrimSpace(runID)
	resp := runDeleteResponse{RunID: runID}
	runDir, err := resolveRunDirForDelete(s.outputRoot, runID)
	if err != nil {
		return resp, http.StatusBadRequest, err
	}

	if err := s.prepareRunForDelete(runID, force); err != nil {
		return resp, http.StatusConflict, err
	}

	if err := cleanupDockerRunContainers(ctx, runID, force); err != nil {
		return resp, http.StatusConflict, err
	}

	if _, err := os.Stat(runDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			resp.Status = "already_deleted"
			resp.Warnings = append(resp.Warnings, s.deleteRunDBRows(ctx, runID)...)
			s.forgetDeletedRun(runID)
			return resp, http.StatusOK, nil
		}
		return resp, http.StatusInternalServerError, fmt.Errorf("stat run dir: %w", err)
	}

	outcome, err := deleteRunDirectory(runDir, filepath.Join(s.outputRoot, ".trash", "runs"), runID)
	if err != nil {
		return resp, http.StatusInternalServerError, err
	}

	resp.Status = outcome.Status
	resp.DeletePending = outcome.DeletePending
	resp.TrashPath = outcome.TrashPath
	resp.Warnings = append(resp.Warnings, s.deleteRunDBRows(ctx, runID)...)
	s.forgetDeletedRun(runID)

	if outcome.DeletePending {
		return resp, http.StatusAccepted, nil
	}
	return resp, http.StatusOK, nil
}

func (s *Server) prepareRunForDelete(runID string, force bool) error {
	entry, ok := s.mgr.Get(runID)
	if !ok {
		return nil
	}
	entry.mu.RLock()
	status := entry.Status
	done := entry.Done
	entry.mu.RUnlock()
	if status != StatusRunning && status != StatusPending {
		return nil
	}
	if !force {
		return fmt.Errorf("cannot delete a running or pending task")
	}
	if err := s.mgr.Cancel(runID); err != nil {
		return err
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("task is still stopping; try delete again after it exits")
	}
}

func (s *Server) deleteRunDBRows(ctx context.Context, runID string) []string {
	if s.db == nil {
		return nil
	}
	if err := s.db.DeleteRun(ctx, runID); err != nil {
		return []string{"数据库索引清理失败: " + err.Error()}
	}
	return nil
}

func (s *Server) forgetDeletedRun(runID string) {
	s.mgr.mu.Lock()
	delete(s.mgr.runs, runID)
	s.mgr.mu.Unlock()

	s.cacheMu.Lock()
	s.runsCache = nil
	s.cacheMu.Unlock()
}

func resolveRunDirForDelete(outputRoot, runID string) (string, error) {
	if runID == "" || runID == "." || runID == ".." {
		return "", fmt.Errorf("invalid run id")
	}
	if strings.ContainsAny(runID, `/\`) {
		return "", fmt.Errorf("invalid run id: path separators are not allowed")
	}
	runsRoot, err := filepath.Abs(filepath.Join(outputRoot, "runs"))
	if err != nil {
		return "", err
	}
	runDir, err := filepath.Abs(filepath.Join(runsRoot, runID))
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(runsRoot, runDir) || runDir == runsRoot {
		return "", fmt.Errorf("invalid run id: path escapes runs root")
	}
	return runDir, nil
}

func deleteRunDirectory(runDir, trashRoot, runID string) (runDirectoryDeleteOutcome, error) {
	if err := removeAllWithRetry(runDir, 5, 150*time.Millisecond); err == nil {
		return runDirectoryDeleteOutcome{Status: "deleted"}, nil
	}
	removeErr := removeAllWithRetry(runDir, 1, 0)
	if removeErr == nil {
		return runDirectoryDeleteOutcome{Status: "deleted"}, nil
	}
	if statErr := pathMissing(runDir); statErr == nil {
		return runDirectoryDeleteOutcome{Status: "deleted"}, nil
	}

	if err := os.MkdirAll(trashRoot, 0o755); err != nil {
		return runDirectoryDeleteOutcome{}, fmt.Errorf("delete failed: %w; also failed to create trash dir: %w", removeErr, err)
	}
	trashDir := filepath.Join(trashRoot, safeTrashName(runID)+"."+time.Now().UTC().Format("20060102T150405.000000000Z"))
	makeTreeWritable(runDir)
	if err := os.Rename(runDir, trashDir); err != nil {
		return runDirectoryDeleteOutcome{}, fmt.Errorf("delete failed: %w; move to background cleanup failed: %w", removeErr, err)
	}
	go retryRemoveTrash(trashDir)
	return runDirectoryDeleteOutcome{Status: "delete_pending", DeletePending: true, TrashPath: trashDir}, nil
}

func removeAllWithRetry(path string, attempts int, delay time.Duration) error {
	if attempts <= 0 {
		attempts = 1
	}
	var last error
	for i := 0; i < attempts; i++ {
		makeTreeWritable(path)
		if err := os.RemoveAll(path); err != nil {
			last = err
		} else if err := pathMissing(path); err == nil {
			return nil
		} else {
			last = err
		}
		if i+1 < attempts && delay > 0 {
			time.Sleep(delay * time.Duration(i+1))
		}
	}
	if last == nil {
		last = fmt.Errorf("path still exists after remove")
	}
	return last
}

func pathMissing(path string) error {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("path still exists: %s", path)
}

func makeTreeWritable(root string) {
	_ = os.Chmod(root, 0o777)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(path, 0o777)
		} else {
			_ = os.Chmod(path, 0o666)
		}
		return nil
	})
}

func retryRemoveTrash(path string) {
	for i := 0; i < 60; i++ {
		if err := removeAllWithRetry(path, 1, 0); err == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func safeTrashName(runID string) string {
	var b strings.Builder
	for _, r := range runID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "run"
	}
	return b.String()
}

func cleanupDockerRunContainers(ctx context.Context, runID string, force bool) error {
	mainName := "utbench-" + runID
	if running, exists, err := dockerContainerRunning(ctx, mainName); err == nil && exists {
		if running && !force {
			return fmt.Errorf("Docker container %s is still running; cancel the run or delete with force", mainName)
		}
		if force || !running {
			_ = dockerRM(ctx, []string{mainName})
		}
	}

	runningSandboxes := dockerContainerIDs(ctx, false, "label=utbench-run="+runID)
	if len(runningSandboxes) > 0 && !force {
		return fmt.Errorf("sandbox containers are still running for %s; cancel the run or delete with force", runID)
	}
	allSandboxes := dockerContainerIDs(ctx, true, "label=utbench-run="+runID)
	if len(allSandboxes) > 0 && (force || len(runningSandboxes) == 0) {
		_ = dockerRM(ctx, allSandboxes)
	}
	return nil
}

func dockerContainerRunning(parent context.Context, name string) (bool, bool, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", name)
	hideCommandWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "no such object") {
			return false, false, nil
		}
		return false, false, err
	}
	return strings.TrimSpace(string(out)) == "true", true, nil
}

func dockerContainerIDs(parent context.Context, all bool, filter string) []string {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	args := []string{"ps", "-q", "--filter", filter}
	if all {
		args = []string{"ps", "-a", "-q", "--filter", filter}
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	hideCommandWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func dockerRM(parent context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	args := append([]string{"rm", "-f"}, ids...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	if runtime.GOOS == "windows" {
		hideCommandWindow(cmd)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm -f: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
