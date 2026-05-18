package web

import (
	"errors"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"
)

func TestAnalysisJobLifecycle(t *testing.T) {
	job := &analysisJob{
		JobID:         "job-1",
		RunID:         "run-1",
		Status:        "queued",
		Phase:         "排队中",
		SelectedCount: 2,
		StartedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if snap := job.snapshot(); snap.Status != "queued" {
		t.Fatalf("initial status = %s", snap.Status)
	}
	job.setRunning("准备证据")
	if snap := job.snapshot(); snap.Status != "running" || snap.Phase != "准备证据" {
		t.Fatalf("running snapshot = %+v", snap)
	}
	job.setPhase("LLM 生成中")
	if snap := job.snapshot(); snap.Phase != "LLM 生成中" {
		t.Fatalf("phase snapshot = %+v", snap)
	}
	job.finish(&contracts.AnalysisReport{RunID: "run-1"}, nil)
	if snap := job.snapshot(); snap.Status != "succeeded" || snap.Report == nil {
		t.Fatalf("success snapshot = %+v", snap)
	}

	failed := &analysisJob{JobID: "job-2", RunID: "run-1", StartedAt: time.Now(), UpdatedAt: time.Now()}
	failed.finish(nil, errors.New("boom"))
	if snap := failed.snapshot(); snap.Status != "failed" || snap.Error != "boom" {
		t.Fatalf("failed snapshot = %+v", snap)
	}
}

func TestCleanupAnalysisJobsDropsExpiredCompletedJobs(t *testing.T) {
	now := time.Now()
	oldEnded := now.Add(-analysisJobRetention - time.Minute)
	recentEnded := now.Add(-time.Minute)
	s := &Server{
		analysisJobs: map[string]*analysisJob{
			"old": {
				JobID:     "old",
				RunID:     "run-1",
				Status:    "succeeded",
				StartedAt: oldEnded.Add(-time.Second),
				UpdatedAt: oldEnded,
				EndedAt:   &oldEnded,
			},
			"recent": {
				JobID:     "recent",
				RunID:     "run-1",
				Status:    "succeeded",
				StartedAt: recentEnded.Add(-time.Second),
				UpdatedAt: recentEnded,
				EndedAt:   &recentEnded,
			},
			"running": {
				JobID:     "running",
				RunID:     "run-1",
				Status:    "running",
				StartedAt: now.Add(-time.Minute),
				UpdatedAt: now,
			},
		},
	}
	s.cleanupAnalysisJobsLocked(now)
	if _, ok := s.analysisJobs["old"]; ok {
		t.Fatal("expired completed job was not removed")
	}
	if _, ok := s.analysisJobs["recent"]; !ok {
		t.Fatal("recent completed job was removed")
	}
	if _, ok := s.analysisJobs["running"]; !ok {
		t.Fatal("running job was removed")
	}
}
