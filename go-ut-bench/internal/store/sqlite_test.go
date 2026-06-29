package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"

	_ "modernc.org/sqlite"
)

func TestIngestEvaluationUpsertV2(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "utbench.db")

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}

	pass := true
	pc := 1
	tc := 2
	pr := 0.5
	set := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          "run_1",
		EvaluatedAtUTC: time.Now().UTC(),
		Results: []contracts.EvaluationResult{
			{
				Model:          "deepseek",
				Language:       "python",
				SampleID:       "s1",
				CompilePass:    true,
				TestPass:       &pass,
				TestPassCount:  &pc,
				TestTotalCount: &tc,
				TestPassRate:   &pr,
			},
		},
	}
	if err := s.IngestEvaluation(ctx, set); err != nil {
		t.Fatal(err)
	}

	pc2 := 2
	tc2 := 2
	pr2 := 1.0
	set.Results[0].TestPassCount = &pc2
	set.Results[0].TestTotalCount = &tc2
	set.Results[0].TestPassRate = &pr2
	set.EvaluatedAtUTC = set.EvaluatedAtUTC.Add(time.Minute)
	if err := s.IngestEvaluation(ctx, set); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var cnt int
	if err := db.QueryRow(`SELECT COUNT(*) FROM evaluation_results WHERE run_id='run_1' AND model='deepseek' AND language='python' AND sample_id='s1'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("expected one upserted row, got %d", cnt)
	}

	if err := db.QueryRow(`SELECT COUNT(*) FROM evaluation_runs WHERE run_id='run_1'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("expected one upserted evaluation run, got %d", cnt)
	}

	var gotRate float64
	if err := db.QueryRow(`SELECT test_pass_rate FROM evaluation_results WHERE run_id='run_1' AND model='deepseek' AND language='python' AND sample_id='s1'`).Scan(&gotRate); err != nil {
		t.Fatal(err)
	}
	if gotRate != 1.0 {
		t.Fatalf("expected updated test_pass_rate=1.0, got %f", gotRate)
	}
}

func TestIngestManifestAndOverview(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "utbench.db")
	runDir := filepath.Join(tmp, "artifacts", "runs", "run_manifest")
	srcPath := filepath.Join(tmp, "datasets", "python", "python_code_files_self_contained", "simple_function", "simple_function_000.py")
	promptPath := filepath.Join(runDir, "generated", "prompts", "rendered", "deepseek", "python", "simple_function_000.md")
	testPath := filepath.Join(runDir, "generated", "tests", "deepseek", "python", "simple_function_000_test.py")
	responsePath := filepath.Join(runDir, "generated", "responses", "deepseek", "python", "simple_function_000.json")
	metadataPath := filepath.Join(runDir, "generated", "metadata", "deepseek", "python", "simple_function_000.json")
	writeTestFile(t, srcPath, "def add(a, b):\n    return a + b\n")
	writeTestFile(t, promptPath, "Write tests")
	writeTestFile(t, testPath, "from module_under_test import add\n")
	writeTestFile(t, responsePath, `{"content":"ok"}`)
	writeTestFile(t, metadataPath, `{"latency_ms":12}`)

	manifest := contracts.GeneratedManifest{
		SchemaVersion:   contracts.SchemaVersion,
		RunID:           "run_manifest",
		CreatedAtUTC:    time.Now().UTC(),
		PromptStrategy:  "fairness-v1",
		PromptVersionID: "prompt-v1",
		Spec: contracts.RunSpec{
			RunID:          "run_manifest",
			Models:         []string{"deepseek"},
			Languages:      []string{"python"},
			DatasetClasses: []string{"self_contained"},
			OutputRoot:     filepath.Join(tmp, "artifacts"),
		},
		Cases: []contracts.GeneratedCase{
			{
				Model:             "deepseek",
				Language:          "python",
				SampleID:          "simple_function_000",
				SamplePath:        srcPath,
				PromptVersionID:   "prompt-v1",
				PromptMode:        "self_contained",
				PromptPath:        promptPath,
				GeneratedTestPath: testPath,
				ResponsePath:      responsePath,
				MetadataPath:      metadataPath,
				GeneratedAtUTC:    time.Now().UTC(),
				Success:           true,
			},
		},
	}
	manifestPath := filepath.Join(runDir, "generated", "generated_manifest.json")
	if err := contracts.WriteJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	pass := true
	evalPath := filepath.Join(runDir, "evaluation", "evaluation_result.json")
	evalSet := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          "run_manifest",
		EvaluatedAtUTC: time.Now().UTC(),
		ManifestPath:   manifestPath,
		Results: []contracts.EvaluationResult{
			{
				Model:             "deepseek",
				Language:          "python",
				SampleID:          "simple_function_000",
				GeneratedTestPath: testPath,
				SourcePath:        srcPath,
				CompilePass:       true,
				TestPass:          &pass,
			},
		},
	}
	if err := contracts.WriteJSON(evalPath, evalSet); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(runDir, "report", "report_summary.json")
	report := contracts.ReportPayload{
		SchemaVersion:    contracts.SchemaVersion,
		RunID:            "run_manifest",
		GeneratedAtUTC:   time.Now().UTC(),
		SourceEvaluation: evalPath,
		Summary:          contracts.ReportSummary{TotalSamples: 1, EligibleSamples: 1},
	}
	if err := contracts.WriteJSON(reportPath, report); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runDir, "report", "report.html"), "<html></html>")

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	sum, err := s.IngestManifestFile(ctx, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !sum.ManifestIngested || sum.GenerationCases != 1 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	runSum, err := s.IngestRun(ctx, IngestRunOptions{RunDir: runDir})
	if err != nil {
		t.Fatal(err)
	}
	if !runSum.EvaluationIngested || !runSum.ReportIngested || runSum.EvaluationResults != 1 {
		t.Fatalf("unexpected run summary: %+v", runSum)
	}
	overview, err := s.Overview(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if overview.GenerationRuns != 1 || overview.GeneratedCases != 1 || overview.EvaluationResults != 1 || overview.Reports != 1 || overview.Artifacts < 7 {
		t.Fatalf("unexpected overview: %+v", overview)
	}

	selected, err := s.SelectEvaluationResultSet(ctx, "db_report", DBReportFilter{RunIDs: []string{"run_manifest"}})
	if err != nil {
		t.Fatal(err)
	}
	if selected.RunID != "db_report" || len(selected.Results) != 1 {
		t.Fatalf("unexpected selected result set: %+v", selected)
	}
	if selected.Results[0].GeneratedTestPath == "" || selected.Results[0].SourcePath == "" {
		t.Fatalf("expected selected rows to keep artifact paths: %+v", selected.Results[0])
	}
}

func TestPromoteGeneratedSetFromManifest(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "utbench.db")
	runDir := filepath.Join(tmp, "artifacts", "runs", "run_set")
	srcPath := filepath.Join(tmp, "datasets", "python", "sample.py")
	testPath := filepath.Join(runDir, "generated", "tests", "deepseek", "python", "sample_test.py")
	failedMetadataPath := filepath.Join(runDir, "generated", "metadata", "deepseek", "python", "failed.json")
	writeTestFile(t, srcPath, "def add(a, b):\n    return a + b\n")
	writeTestFile(t, testPath, "from sample import add\n")
	writeTestFile(t, failedMetadataPath, `{"success":false}`)

	tokens := 42
	manifest := contracts.GeneratedManifest{
		SchemaVersion:   contracts.SchemaVersion,
		RunID:           "run_set",
		CreatedAtUTC:    time.Now().UTC(),
		PromptStrategy:  "structured-v1",
		PromptVersionID: "prompt-v1",
		Spec: contracts.RunSpec{
			RunID:          "run_set",
			Models:         []string{"deepseek"},
			Languages:      []string{"python"},
			DatasetClasses: []string{"self_contained"},
			OutputRoot:     filepath.Join(tmp, "artifacts"),
		},
		Cases: []contracts.GeneratedCase{
			{
				Model:             "deepseek",
				Language:          "python",
				SampleID:          "sample_ok",
				SamplePath:        srcPath,
				GeneratedTestPath: testPath,
				TotalTokens:       &tokens,
				GeneratedAtUTC:    time.Now().UTC(),
				Success:           true,
			},
			{
				Model:          "deepseek",
				Language:       "python",
				SampleID:       "sample_failed",
				SamplePath:     srcPath,
				MetadataPath:   failedMetadataPath,
				GeneratedAtUTC: time.Now().UTC(),
				Success:        false,
			},
		},
	}
	manifestPath := filepath.Join(runDir, "generated", "generated_manifest.json")
	if err := contracts.WriteJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	set, err := s.PromoteGeneratedSetFromManifest(ctx, PromoteGeneratedSetOptions{
		Name:               "smoke set",
		Note:               "usable generation set",
		SourceManifestPath: manifestPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.SampleCount != 2 || set.AcceptedCount != 1 || set.SuccessCount != 1 || set.FailureCount != 1 {
		t.Fatalf("unexpected generated set counts: %+v", set)
	}
	if set.TotalTokens == nil || *set.TotalTokens != 42 {
		t.Fatalf("unexpected token summary: %+v", set.TotalTokens)
	}
	samples, err := s.ListGeneratedSetSamples(ctx, set.GeneratedSetID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 generated set samples, got %d", len(samples))
	}
	if samples[0].GeneratedTestPath == "" && samples[1].GeneratedTestPath == "" {
		t.Fatalf("expected generated test path to be indexed: %+v", samples)
	}
	curated, err := contracts.ReadGeneratedManifest(set.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(curated.Cases) != 1 || curated.Cases[0].SampleID != "sample_ok" {
		t.Fatalf("unexpected curated manifest cases: %+v", curated.Cases)
	}
	if curated.Cases[0].GeneratedTestPath == testPath {
		t.Fatalf("expected curated manifest to point at copied generated test, got source path %s", curated.Cases[0].GeneratedTestPath)
	}
	if _, err := os.Stat(curated.Cases[0].GeneratedTestPath); err != nil {
		t.Fatalf("expected copied generated test file to exist: %v", err)
	}
}

func TestFindReusableGeneratedAssetByIdentityAllowsImageDigestChange(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "utbench.db")
	runDir := filepath.Join(tmp, "artifacts", "runs", "run_old")
	srcPath := filepath.Join(tmp, "datasets", "go", "go_code_files_repo_level", "oss", "demo", "color.go")
	promptPath := filepath.Join(runDir, "generated", "prompts", "rendered", "opencode__deepseek__no_skill", "go", "color.prompt.txt")
	testPath := filepath.Join(runDir, "generated", "tests", "opencode__deepseek__no_skill", "go", "color.test.go")
	responsePath := filepath.Join(runDir, "generated", "metadata", "color.response.json")
	metadataPath := filepath.Join(runDir, "generated", "metadata", "color.metadata.json")
	writeTestFile(t, srcPath, "package color\n\nfunc Enabled() bool { return true }\n")
	writeTestFile(t, promptPath, "Write tests for color.go")
	writeTestFile(t, testPath, "package color\n\nimport \"testing\"\n\nfunc TestEnabled(t *testing.T) { if !Enabled() { t.Fatal(\"disabled\") } }\n")
	writeTestFile(t, responsePath, `{"content":"ok"}`)
	writeTestFile(t, metadataPath, `{"success":true}`)

	manifest := contracts.GeneratedManifest{
		SchemaVersion:   contracts.SchemaVersion,
		RunID:           "run_old",
		CreatedAtUTC:    time.Now().UTC(),
		PromptStrategy:  "structured-v1",
		PromptVersionID: "prompt-v1",
		Spec: contracts.RunSpec{
			RunID:          "run_old",
			Models:         []string{"deepseek"},
			Subjects:       []string{"opencode__deepseek__no_skill"},
			Languages:      []string{"go"},
			DatasetClasses: []string{"repo_level"},
			OutputRoot:     filepath.Join(tmp, "artifacts"),
		},
		Cases: []contracts.GeneratedCase{
			{
				Model:                    "opencode__deepseek__no_skill",
				SubjectID:                "opencode__deepseek__no_skill",
				SubjectKind:              "cli_agent",
				AgentFramework:           "opencode",
				AgentModel:               "deepseek",
				SkillName:                "no_skill",
				Language:                 "go",
				SampleID:                 "color",
				SampleUID:                "dataset_sample_color",
				SamplePath:               srcPath,
				PromptVersionID:          "prompt-v1",
				PromptMode:               "repo_level",
				PromptPath:               promptPath,
				GeneratedTestPath:        testPath,
				ResponsePath:             responsePath,
				MetadataPath:             metadataPath,
				SandboxFingerprint:       "sandbox-sha",
				SubjectVersionID:         "subject_version_old_digest",
				FrameworkConfigSHA256:    "framework-sha",
				SkillSHA256:              "skill-sha",
				AgentCommandSHA256:       "command-sha",
				DockerImage:              "utbench-agent-base:latest",
				DockerImageDigest:        "sha256:old",
				EnvContractSHA256:        "env-contract-old",
				GenerationKey:            "generation_old_digest",
				DependencyFingerprint:    "dependency-sha",
				GenerationEnvFingerprint: "generation-env-sha",
				GeneratedAtUTC:           time.Now().UTC(),
				Success:                  true,
			},
		},
	}
	manifestPath := filepath.Join(runDir, "generated", "generated_manifest.json")
	if err := contracts.WriteJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestManifestFile(ctx, manifestPath); err != nil {
		t.Fatal(err)
	}
	emptyRunDir := filepath.Join(tmp, "artifacts", "runs", "run_empty")
	emptyTestPath := filepath.Join(emptyRunDir, "generated", "tests", "opencode__deepseek__no_skill", "go", "color.test.go")
	emptyPromptPath := filepath.Join(emptyRunDir, "generated", "prompts", "rendered", "opencode__deepseek__no_skill", "go", "color.prompt.txt")
	emptyResponsePath := filepath.Join(emptyRunDir, "generated", "metadata", "color.response.json")
	emptyMetadataPath := filepath.Join(emptyRunDir, "generated", "metadata", "color.metadata.json")
	writeTestFile(t, emptyTestPath, "")
	writeTestFile(t, emptyPromptPath, "Write tests for color.go")
	writeTestFile(t, emptyResponsePath, `{"content":""}`)
	writeTestFile(t, emptyMetadataPath, `{"success":true}`)
	emptyManifest := manifest
	emptyManifest.RunID = "run_empty"
	emptyManifest.Spec.RunID = "run_empty"
	emptyManifest.CreatedAtUTC = time.Now().UTC().Add(time.Hour)
	emptyManifest.Cases[0].GeneratedTestPath = emptyTestPath
	emptyManifest.Cases[0].PromptPath = emptyPromptPath
	emptyManifest.Cases[0].ResponsePath = emptyResponsePath
	emptyManifest.Cases[0].MetadataPath = emptyMetadataPath
	emptyManifest.Cases[0].GeneratedAtUTC = emptyManifest.CreatedAtUTC
	emptyManifestPath := filepath.Join(emptyRunDir, "generated", "generated_manifest.json")
	if err := contracts.WriteJSON(emptyManifestPath, emptyManifest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestManifestFile(ctx, emptyManifestPath); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.FindReusableGeneratedAsset(ctx, "generation_new_digest"); err != nil || ok {
		t.Fatalf("exact key lookup = ok:%v err:%v, want miss without error", ok, err)
	}

	reused, ok, err := s.FindReusableGeneratedAssetByIdentity(
		ctx,
		"opencode__deepseek__no_skill",
		"go",
		"dataset_sample_color",
		"prompt-v1",
		"repo_level",
		"framework-sha",
		"skill-sha",
		"command-sha",
		"dependency-sha",
		"generation-env-sha",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected logical identity lookup to reuse old successful generation")
	}
	if reused.RunID != "run_old" || reused.GeneratedTestPath == "" {
		t.Fatalf("unexpected reused case: %+v", reused)
	}
	if _, ok, err := s.FindReusableGeneratedAssetByIdentity(
		ctx,
		"opencode__deepseek__no_skill",
		"go",
		"dataset_sample_color",
		"prompt-v1",
		"repo_level",
		"different-framework-sha",
		"skill-sha",
		"command-sha",
		"dependency-sha",
		"generation-env-sha",
	); err != nil || ok {
		t.Fatalf("mismatched framework lookup = ok:%v err:%v, want miss without error", ok, err)
	}
}

func TestIngestManifestDatasetSampleUIDConflicts(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "utbench.db")

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}

	pathA := filepath.Join(tmp, "datasets", "go", "go_code_files_repo_level", "oss", "demo", "pkg", "a.go")
	pathB := filepath.Join(tmp, "datasets", "go", "go_code_files_repo_level", "oss", "demo", "pkg", "b.go")
	pathC := filepath.Join(tmp, "datasets", "go", "go_code_files_repo_level", "oss", "demo", "pkg", "c.go")
	writeTestFile(t, pathA, "package pkg\n\nfunc A() int { return 1 }\n")
	writeTestFile(t, pathB, "package pkg\n\nfunc B() int { return 2 }\n")
	writeTestFile(t, pathC, "package pkg\n\nfunc C() int { return 3 }\n")

	if err := ingestManifestForSampleUIDConflict(t, ctx, s, tmp, manifestForSampleUIDConflict("run_uid_old", "dataset_sample_same", "old_sample", pathA)); err != nil {
		t.Fatal(err)
	}
	if err := ingestManifestForSampleUIDConflict(t, ctx, s, tmp, manifestForSampleUIDConflict("run_uid_new_path", "dataset_sample_same", "new_sample", pathB)); err != nil {
		t.Fatal(err)
	}
	if err := ingestManifestForSampleUIDConflict(t, ctx, s, tmp, manifestForSampleUIDConflict("run_uid_new_value", "dataset_sample_new", "new_sample", pathB)); err != nil {
		t.Fatal(err)
	}
	if err := ingestManifestForSampleUIDConflict(t, ctx, s, tmp, manifestForSampleUIDConflict("run_uid_legacy_identity", "dataset_sample_legacy", "legacy_sample", pathC)); err != nil {
		t.Fatal(err)
	}
	if err := ingestManifestForSampleUIDConflict(t, ctx, s, tmp, manifestForSampleUIDConflict("run_uid_merge_identity", "dataset_sample_new", "legacy_sample", pathC)); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var uid, sampleID, samplePath string
	if err := db.QueryRow(`SELECT sample_uid, sample_id, path FROM dataset_samples WHERE language='go' AND sample_id='legacy_sample' AND path=?`, pathC).Scan(&uid, &sampleID, &samplePath); err != nil {
		t.Fatal(err)
	}
	if uid != "dataset_sample_new" || sampleID != "legacy_sample" || samplePath != pathC {
		t.Fatalf("unexpected dataset sample row: uid=%s sample_id=%s path=%s", uid, sampleID, samplePath)
	}
	var legacyRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dataset_samples WHERE sample_uid='dataset_sample_legacy'`).Scan(&legacyRows); err != nil {
		t.Fatal(err)
	}
	if legacyRows != 0 {
		t.Fatalf("expected legacy sample uid to be merged, got %d rows", legacyRows)
	}
}

func TestIngestRunReplacesPriorEvaluationAndReportForSameRun(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "utbench.db")

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}

	runID := "run_replace"
	runDir := filepath.Join(tmp, "artifacts", "runs", runID)
	evalPath := filepath.Join(runDir, "evaluation", "evaluation_result.json")
	reportPath := filepath.Join(runDir, "report", "report_summary.json")
	reportHTMLPath := filepath.Join(runDir, "report", "report.html")

	writeEvaluationAndReport := func(pass bool, sampleIDs []string, generatedAt time.Time) {
		t.Helper()
		testPass := pass
		results := make([]contracts.EvaluationResult, 0, len(sampleIDs))
		for _, sampleID := range sampleIDs {
			results = append(results, contracts.EvaluationResult{
				Model:             "deepseek",
				Language:          "python",
				SampleID:          sampleID,
				GeneratedTestPath: filepath.Join(runDir, "generated", "tests", sampleID+"_test.py"),
				SourcePath:        filepath.Join(tmp, "datasets", "python", "self_contained", sampleID+".py"),
				CompilePass:       pass,
				TestPass:          &testPass,
			})
		}
		set := contracts.EvaluationResultSet{
			SchemaVersion:  contracts.SchemaVersion,
			RunID:          runID,
			EvaluatedAtUTC: generatedAt,
			Results:        results,
		}
		if err := contracts.WriteJSON(evalPath, set); err != nil {
			t.Fatal(err)
		}
		report := contracts.ReportPayload{
			SchemaVersion:    contracts.SchemaVersion,
			RunID:            runID,
			GeneratedAtUTC:   generatedAt,
			SourceEvaluation: evalPath,
			Summary: contracts.ReportSummary{
				TotalSamples:     len(sampleIDs),
				CompilePassCount: map[bool]int{true: 1, false: 0}[pass],
				CompilePassRate:  map[bool]float64{true: 1, false: 0}[pass],
			},
		}
		if err := contracts.WriteJSON(reportPath, report); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(reportHTMLPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(reportHTMLPath, []byte(generatedAt.Format(time.RFC3339Nano)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeEvaluationAndReport(false, []string{"s1", "s2"}, time.Date(2026, 6, 28, 7, 0, 0, 0, time.UTC))
	if _, err := s.IngestRun(ctx, IngestRunOptions{RunDir: runDir}); err != nil {
		t.Fatal(err)
	}
	writeEvaluationAndReport(true, []string{"s1"}, time.Date(2026, 6, 28, 8, 0, 0, 0, time.UTC))
	if _, err := s.IngestRun(ctx, IngestRunOptions{RunDir: runDir}); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"evaluation_runs", "evaluation_results", "report_snapshots"} {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE run_id=?", runID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("expected one current row in %s, got %d", table, count)
		}
	}

	var compilePass int
	if err := s.db.QueryRowContext(ctx, `SELECT compile_pass FROM evaluation_results WHERE run_id=?`, runID).Scan(&compilePass); err != nil {
		t.Fatal(err)
	}
	if compilePass != 1 {
		t.Fatalf("expected latest evaluation result to replace old one, got compile_pass=%d", compilePass)
	}

	var reportCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM report_result_members`).Scan(&reportCount); err != nil {
		t.Fatal(err)
	}
	if reportCount != 0 {
		t.Fatalf("expected stale report members to be removed, got %d", reportCount)
	}
}

func TestDeleteRunRemovesIndexedRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "utbench.db")

	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO generation_runs(run_id, schema_version, created_at_utc, spec_json, created_db_at_utc, updated_db_at_utc)
		VALUES('run_delete', ?, ?, '{}', ?, ?)`, contracts.SchemaVersion, now, now, now)
	exec(`INSERT INTO prompt_renderings(prompt_rendering_id, run_id, model, language, sample_id, created_at_utc)
		VALUES('prompt_delete', 'run_delete', 'deepseek', 'python', 's1', ?)`, now)
	exec(`INSERT INTO generated_cases(generated_case_id, run_id, model, language, sample_id, success, created_db_at_utc, updated_db_at_utc)
		VALUES('case_delete', 'run_delete', 'deepseek', 'python', 's1', 1, ?, ?)`, now, now)
	exec(`INSERT INTO evaluation_runs(evaluation_run_id, run_id, schema_version, evaluated_at_utc, created_db_at_utc, updated_db_at_utc)
		VALUES('eval_delete', 'run_delete', ?, ?, ?, ?)`, contracts.SchemaVersion, now, now, now)
	exec(`INSERT INTO evaluation_results(evaluation_result_id, evaluation_run_id, run_id, model, language, sample_id, compile_pass, created_db_at_utc, updated_db_at_utc)
		VALUES('result_delete', 'eval_delete', 'run_delete', 'deepseek', 'python', 's1', 1, ?, ?)`, now, now)
	exec(`INSERT INTO evaluation_stage_results(stage_result_id, evaluation_result_id, stage, status, created_at_utc)
		VALUES('stage_delete', 'result_delete', 'compile', 'passed', ?)`, now)
	exec(`INSERT INTO report_snapshots(report_id, run_id, generated_at_utc, created_db_at_utc)
		VALUES('report_delete', 'run_delete', ?, ?)`, now, now)
	exec(`INSERT INTO report_result_members(report_id, evaluation_result_id)
		VALUES('report_delete', 'result_delete')`)
	exec(`INSERT INTO artifacts(artifact_id, kind, path, size_bytes, sha256, created_at_utc)
		VALUES('artifact_delete', 'report_html', 'artifacts/runs/run_delete/report/report.html', 1, 'sha-delete', ?)`, now)
	exec(`INSERT INTO run_artifacts(run_id, artifact_id, role, created_at_utc)
		VALUES('run_delete', 'artifact_delete', 'report_html', ?)`, now)

	if err := s.DeleteRun(ctx, "run_delete"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"generation_runs",
		"prompt_renderings",
		"generated_cases",
		"evaluation_runs",
		"evaluation_results",
		"evaluation_stage_results",
		"report_snapshots",
		"report_result_members",
		"run_artifacts",
	} {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("expected %s to be empty, got %d", table, count)
		}
	}
	var deletedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(deleted_at_utc, '') FROM artifacts WHERE artifact_id='artifact_delete'`).Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if deletedAt == "" {
		t.Fatal("expected artifact to be marked deleted")
	}
}

func ingestManifestForSampleUIDConflict(t *testing.T, ctx context.Context, s *SQLiteStore, root string, manifest contracts.GeneratedManifest) error {
	t.Helper()
	manifestPath := filepath.Join(root, "artifacts", "runs", manifest.RunID, "generated", "generated_manifest.json")
	if err := contracts.WriteJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	_, err := s.IngestManifestFile(ctx, manifestPath)
	return err
}

func manifestForSampleUIDConflict(runID, sampleUID, sampleID, samplePath string) contracts.GeneratedManifest {
	return contracts.GeneratedManifest{
		SchemaVersion:   contracts.SchemaVersion,
		RunID:           runID,
		CreatedAtUTC:    time.Now().UTC(),
		PromptStrategy:  "structured-v1",
		PromptVersionID: "prompt-v1",
		Spec: contracts.RunSpec{
			RunID:          runID,
			Models:         []string{"deepseek"},
			Languages:      []string{"go"},
			DatasetClasses: []string{"repo_level"},
		},
		Cases: []contracts.GeneratedCase{
			{
				Model:             "deepseek",
				Language:          "go",
				SampleID:          sampleID,
				SampleUID:         sampleUID,
				SamplePath:        samplePath,
				PromptVersionID:   "prompt-v1",
				PromptMode:        "repo_level",
				GeneratedTestPath: filepath.Join(filepath.Dir(samplePath), sampleID+"_test.go"),
				ResponsePath:      filepath.Join(filepath.Dir(samplePath), sampleID+".response.json"),
				MetadataPath:      filepath.Join(filepath.Dir(samplePath), sampleID+".metadata.json"),
				GeneratedAtUTC:    time.Now().UTC(),
				Success:           true,
			},
		},
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
