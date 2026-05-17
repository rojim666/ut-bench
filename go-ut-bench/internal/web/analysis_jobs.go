package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go-ut-bench/internal/analyzer"
	"go-ut-bench/internal/contracts"
)

const (
	analysisJobRetention = 30 * time.Minute
	analysisJobMaxCount  = 100
)

type analysisJob struct {
	mu            *sync.Mutex
	JobID         string                    `json:"job_id"`
	RunID         string                    `json:"run_id"`
	Status        string                    `json:"status"`
	Phase         string                    `json:"phase"`
	SelectedCount int                       `json:"selected_count"`
	StartedAt     time.Time                 `json:"started_at"`
	UpdatedAt     time.Time                 `json:"updated_at"`
	EndedAt       *time.Time                `json:"ended_at,omitempty"`
	ElapsedMS     int64                     `json:"elapsed_ms"`
	Error         string                    `json:"error,omitempty"`
	Report        *contracts.AnalysisReport `json:"report,omitempty"`
}

func (s *Server) handleRunAnalysisJob(w http.ResponseWriter, r *http.Request, runID, suffix string) {
	suffix = strings.Trim(suffix, "/")
	switch {
	case suffix == "" && r.Method == http.MethodPost:
		s.createRunAnalysisJob(w, r, runID)
	case suffix != "" && r.Method == http.MethodGet:
		s.getRunAnalysisJob(w, r, runID, suffix)
	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) createRunAnalysisJob(w http.ResponseWriter, r *http.Request, runID string) {
	var req runAnalysisRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if len(req.SelectedSubjects) > 3 {
		errJSON(w, http.StatusBadRequest, "selected_subjects supports at most 3 items")
		return
	}
	now := time.Now()
	job := &analysisJob{
		JobID:         newAnalysisJobID(),
		mu:            &sync.Mutex{},
		RunID:         runID,
		Status:        "queued",
		Phase:         "排队中",
		SelectedCount: len(req.SelectedSubjects),
		StartedAt:     now,
		UpdatedAt:     now,
	}
	s.analysisJobsMu.Lock()
	s.cleanupAnalysisJobsLocked(now)
	s.analysisJobs[job.JobID] = job
	s.analysisJobsMu.Unlock()

	go s.runAnalysisJob(job.JobID, req)
	writeJSON(w, http.StatusAccepted, job.snapshot())
}

func (s *Server) getRunAnalysisJob(w http.ResponseWriter, _ *http.Request, runID, jobID string) {
	job := s.lookupAnalysisJob(jobID)
	if job == nil || job.RunID != runID {
		errJSON(w, http.StatusNotFound, "analysis job not found")
		return
	}
	writeJSON(w, http.StatusOK, job.snapshot())
}

func (s *Server) runAnalysisJob(jobID string, req runAnalysisRequest) {
	job := s.lookupAnalysisJob(jobID)
	if job == nil {
		return
	}
	job.setRunning("准备证据")
	report, err := analyzer.NewService().Analyze(context.Background(), analyzer.Options{
		RunID:              job.RunID,
		OutputRoot:         s.outputRoot,
		ConfigPath:         s.configPath,
		LLMEnabled:         req.LLMEnabled,
		LLMModel:           req.LLMModel,
		Force:              req.Force,
		SkipReportInsights: req.SkipReportInsights,
		SelectedSubjects:   req.SelectedSubjects,
		CompareMode:        req.CompareMode,
		Progress: func(phase string) {
			job.setPhase(phase)
		},
	})
	if err != nil {
		job.finish(nil, err)
		return
	}
	job.finish(report, nil)
}

func (s *Server) lookupAnalysisJob(jobID string) *analysisJob {
	s.analysisJobsMu.RLock()
	defer s.analysisJobsMu.RUnlock()
	return s.analysisJobs[jobID]
}

func (s *Server) cleanupAnalysisJobsLocked(now time.Time) {
	for id, job := range s.analysisJobs {
		snap := job.snapshot()
		if snap.EndedAt != nil && now.Sub(*snap.EndedAt) > analysisJobRetention {
			delete(s.analysisJobs, id)
		}
	}
	if len(s.analysisJobs) <= analysisJobMaxCount {
		return
	}
	type endedJob struct {
		id      string
		endedAt time.Time
	}
	var ended []endedJob
	for id, job := range s.analysisJobs {
		snap := job.snapshot()
		if snap.EndedAt != nil {
			ended = append(ended, endedJob{id: id, endedAt: *snap.EndedAt})
		}
	}
	sort.Slice(ended, func(i, j int) bool {
		return ended[i].endedAt.Before(ended[j].endedAt)
	})
	for _, item := range ended {
		if len(s.analysisJobs) <= analysisJobMaxCount {
			break
		}
		delete(s.analysisJobs, item.id)
	}
}

func (j *analysisJob) setRunning(phase string) {
	j.ensureMu()
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "running"
	j.setPhaseLocked(phase)
}

func (j *analysisJob) setPhase(phase string) {
	j.ensureMu()
	j.mu.Lock()
	defer j.mu.Unlock()
	j.setPhaseLocked(phase)
}

func (j *analysisJob) setPhaseLocked(phase string) {
	if strings.TrimSpace(phase) == "" {
		return
	}
	j.Phase = phase
	j.UpdatedAt = time.Now()
	j.ElapsedMS = time.Since(j.StartedAt).Milliseconds()
}

func (j *analysisJob) finish(report *contracts.AnalysisReport, err error) {
	j.ensureMu()
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.UpdatedAt = now
	j.EndedAt = &now
	j.ElapsedMS = time.Since(j.StartedAt).Milliseconds()
	if err != nil {
		j.Status = "failed"
		j.Phase = "失败"
		j.Error = err.Error()
		return
	}
	j.Status = "succeeded"
	j.Phase = "完成"
	j.Report = report
}

func (j *analysisJob) snapshot() analysisJob {
	j.ensureMu()
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ElapsedMS = time.Since(j.StartedAt).Milliseconds()
	cp := *j
	cp.mu = nil
	return cp
}

func (j *analysisJob) ensureMu() {
	if j.mu == nil {
		j.mu = &sync.Mutex{}
	}
}

func newAnalysisJobID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}
