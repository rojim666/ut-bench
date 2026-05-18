package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-ut-bench/internal/contracts"
)

type PromoteGeneratedSetOptions struct {
	GeneratedSetID     string
	Name               string
	Note               string
	Status             string
	SourceRunID        string
	SourceManifestPath string
	ManifestPath       string
	DockerManifestPath string
	OutputDir          string
}

type UpdateGeneratedSetOptions struct {
	Name               string
	Note               string
	Status             string
	ManifestPath       string
	DockerManifestPath string
}

type UpdateGeneratedSetSampleOptions struct {
	Status   string
	Note     string
	TagsJSON string
}

func (s *SQLiteStore) PromoteGeneratedSetFromManifest(ctx context.Context, opts PromoteGeneratedSetOptions) (GeneratedSetItem, error) {
	sourceManifestPath := strings.TrimSpace(opts.SourceManifestPath)
	if sourceManifestPath == "" {
		return GeneratedSetItem{}, fmt.Errorf("source manifest path is required")
	}
	sourceManifestPath = resolveStoredPath(sourceManifestPath)
	manifest, err := contracts.ReadGeneratedManifest(sourceManifestPath)
	if err != nil {
		return GeneratedSetItem{}, err
	}
	sourceRunID := firstNonEmpty(opts.SourceRunID, manifest.RunID)
	if strings.TrimSpace(sourceRunID) == "" {
		return GeneratedSetItem{}, fmt.Errorf("source run id is required")
	}

	// 确保 generated_cases 和 artifact 索引先落库，生成集只引用统一的生成明细。
	if _, err := s.IngestManifestFile(ctx, sourceManifestPath); err != nil {
		return GeneratedSetItem{}, err
	}

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "生成集 " + sourceRunID
	}
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "candidate"
	}
	setID := strings.TrimSpace(opts.GeneratedSetID)
	if setID == "" {
		setID = stableID("generated_set", sourceRunID, name)
	}
	outputDir := strings.TrimSpace(opts.OutputDir)
	if outputDir == "" {
		outputDir = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(sourceManifestPath)))), "generated_sets", setID)
	}
	manifestPath := strings.TrimSpace(opts.ManifestPath)
	if manifestPath == "" {
		manifestPath = filepath.Join(outputDir, "generated_manifest.json")
	}

	modelSummary, languageSummary := summarizeGeneratedSetCases(manifest.Cases)
	totalTokens := generatedSetTotalTokens(manifest.Cases)
	successCount := 0
	acceptedCases := make([]contracts.GeneratedCase, 0, len(manifest.Cases))
	copiedTests := map[string]string{}
	copiedSHAs := map[string]string{}
	for _, c := range manifest.Cases {
		if c.Success {
			successCount++
		}
		if !generatedCaseAccepted(c) {
			continue
		}
		copiedPath, copiedSHA, ok, err := copyGeneratedTestToSet(sourceManifestPath, outputDir, c)
		if err != nil {
			return GeneratedSetItem{}, err
		}
		if ok {
			c.GeneratedTestPath = copiedPath
			acceptedCases = append(acceptedCases, c)
			copiedTests[caseKey(c.Model, c.Language, c.SampleID)] = copiedPath
			copiedSHAs[caseKey(c.Model, c.Language, c.SampleID)] = copiedSHA
		}
	}
	curated := manifest
	curated.Cases = acceptedCases
	if err := contracts.WriteJSON(manifestPath, curated); err != nil {
		return GeneratedSetItem{}, fmt.Errorf("write generated set manifest: %w", err)
	}

	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GeneratedSetItem{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO generated_sets(generated_set_id, name, note, status, source_run_id,
			source_manifest_path, manifest_path, docker_manifest_path, output_dir,
			model_summary, language_summary, sample_count, accepted_count, success_count, failure_count,
			total_tokens, prompt_strategy, prompt_version_id, created_at_utc, updated_at_utc)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(generated_set_id) DO UPDATE SET
			name=excluded.name,
			note=excluded.note,
			status=excluded.status,
			source_manifest_path=excluded.source_manifest_path,
			manifest_path=excluded.manifest_path,
			docker_manifest_path=excluded.docker_manifest_path,
			output_dir=excluded.output_dir,
			model_summary=excluded.model_summary,
			language_summary=excluded.language_summary,
			sample_count=excluded.sample_count,
			accepted_count=excluded.accepted_count,
			success_count=excluded.success_count,
			failure_count=excluded.failure_count,
			total_tokens=excluded.total_tokens,
			prompt_strategy=excluded.prompt_strategy,
			prompt_version_id=excluded.prompt_version_id,
			updated_at_utc=excluded.updated_at_utc`,
		setID, name, nullString(opts.Note), status, sourceRunID,
		portablePath(sourceManifestPath), portablePath(manifestPath), nullString(portablePath(opts.DockerManifestPath)), portablePath(outputDir),
		modelSummary, languageSummary, len(manifest.Cases), len(acceptedCases), successCount, len(manifest.Cases)-successCount,
		nullableInt(totalTokens), nullString(manifest.PromptStrategy), nullString(manifest.PromptVersionID), now, now); err != nil {
		return GeneratedSetItem{}, fmt.Errorf("upsert generated set: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM generated_set_samples WHERE generated_set_id=?`, setID); err != nil {
		return GeneratedSetItem{}, fmt.Errorf("clear generated set samples: %w", err)
	}
	for _, c := range manifest.Cases {
		caseID := stableID("generated_case", sourceRunID, c.Model, c.Language, c.SampleID)
		status := "rejected"
		if copiedTests[caseKey(c.Model, c.Language, c.SampleID)] != "" {
			status = "accepted"
		}
		artifactID, artifactPath, sha, err := generatedCaseArtifact(ctx, tx, caseID)
		if err != nil {
			return GeneratedSetItem{}, err
		}
		generatedTestPath := firstNonEmpty(artifactPath, c.GeneratedTestPath)
		if copied := copiedTests[caseKey(c.Model, c.Language, c.SampleID)]; copied != "" {
			generatedTestPath = copied
		}
		if copiedSHA := copiedSHAs[caseKey(c.Model, c.Language, c.SampleID)]; copiedSHA != "" {
			sha = copiedSHA
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO generated_set_samples(generated_set_id, generated_case_id, run_id, model, language, sample_id,
				sample_uid, sample_path, generated_test_path, generated_test_artifact_id, generated_test_sha256,
				success, status, note, tags_json, created_at_utc, updated_at_utc)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			setID, caseID, sourceRunID, c.Model, c.Language, c.SampleID,
			nullString(c.SampleUID), nullString(c.SamplePath), nullString(generatedTestPath), nullString(artifactID), nullString(sha),
			boolToInt(c.Success), status, nil, nil, now, now); err != nil {
			return GeneratedSetItem{}, fmt.Errorf("insert generated set sample: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return GeneratedSetItem{}, err
	}
	return s.GetGeneratedSet(ctx, setID)
}

func generatedCaseAccepted(c contracts.GeneratedCase) bool {
	return c.Success && strings.TrimSpace(c.GeneratedTestPath) != ""
}

func copyGeneratedTestToSet(sourceManifestPath, outputDir string, c contracts.GeneratedCase) (string, string, bool, error) {
	src := resolveGeneratedCaseFile(sourceManifestPath, c.GeneratedTestPath)
	if !fileExists(src) {
		return "", "", false, nil
	}
	ext := filepath.Ext(src)
	if ext == "" {
		ext = ".txt"
	}
	fileName := safePathPart(filepath.Base(src))
	if fileName == "unknown" {
		fileName = safePathPart(c.SampleID) + ext
	}
	target := filepath.Join(outputDir, "tests", safePathPart(c.Model), safePathPart(c.Language), safePathPart(c.SampleID), fileName)
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", "", false, err
	}
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return "", "", false, err
	}
	sha, _, err := fileSHA256(target)
	if err != nil {
		return "", "", false, err
	}
	return target, sha, true, nil
}

func resolveGeneratedCaseFile(sourceManifestPath, path string) string {
	p := resolveStoredPath(path)
	if fileExists(p) {
		return p
	}
	if filepath.IsAbs(filepath.FromSlash(path)) {
		return filepath.FromSlash(path)
	}
	runDir := filepath.Dir(filepath.Dir(sourceManifestPath))
	candidate := filepath.Join(runDir, filepath.FromSlash(path))
	if fileExists(candidate) {
		return candidate
	}
	return p
}

func safePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	return replacer.Replace(value)
}

func summarizeGeneratedSetCases(cases []contracts.GeneratedCase) (string, string) {
	models := map[string]struct{}{}
	languages := map[string]struct{}{}
	for _, c := range cases {
		if strings.TrimSpace(c.Model) != "" {
			models[c.Model] = struct{}{}
		}
		if strings.TrimSpace(c.Language) != "" {
			languages[c.Language] = struct{}{}
		}
	}
	return strings.Join(sortedKeys(models), ","), strings.Join(sortedKeys(languages), ",")
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func generatedSetTotalTokens(cases []contracts.GeneratedCase) *int {
	total := 0
	seen := false
	for _, c := range cases {
		if c.TotalTokens != nil {
			total += *c.TotalTokens
			seen = true
		}
	}
	if !seen {
		return nil
	}
	return &total
}

func generatedCaseArtifact(ctx context.Context, tx *sql.Tx, caseID string) (artifactID, artifactPath, sha string, err error) {
	var artifactIDNull, pathNull, shaNull sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(gc.generated_test_artifact_id, ''), COALESCE(a.path, ''), COALESCE(a.sha256, '')
		FROM generated_cases gc
		LEFT JOIN artifacts a ON a.artifact_id = gc.generated_test_artifact_id
		WHERE gc.generated_case_id = ?`, caseID).Scan(&artifactIDNull, &pathNull, &shaNull)
	if err == sql.ErrNoRows {
		return "", "", "", nil
	}
	if err != nil {
		return "", "", "", err
	}
	if artifactIDNull.Valid {
		artifactID = artifactIDNull.String
	}
	if pathNull.Valid {
		artifactPath = pathNull.String
	}
	if shaNull.Valid {
		sha = shaNull.String
	}
	return artifactID, artifactPath, sha, nil
}

func (s *SQLiteStore) ListGeneratedSets(ctx context.Context, status string, limit int) ([]GeneratedSetItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := "1=1"
	args := []any{}
	if strings.TrimSpace(status) != "" {
		where = "status = ?"
		args = append(args, status)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT generated_set_id, name, COALESCE(note, ''), status, source_run_id,
		       COALESCE(source_manifest_path, ''), COALESCE(manifest_path, ''), COALESCE(docker_manifest_path, ''),
		       COALESCE(output_dir, ''), COALESCE(model_summary, ''), COALESCE(language_summary, ''),
		       sample_count, accepted_count, success_count, failure_count, total_tokens,
		       COALESCE(prompt_strategy, ''), COALESCE(prompt_version_id, ''), created_at_utc, updated_at_utc
		FROM generated_sets
		WHERE `+where+`
		ORDER BY updated_at_utc DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GeneratedSetItem
	for rows.Next() {
		item, err := scanGeneratedSet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetGeneratedSet(ctx context.Context, id string) (GeneratedSetItem, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT generated_set_id, name, COALESCE(note, ''), status, source_run_id,
		       COALESCE(source_manifest_path, ''), COALESCE(manifest_path, ''), COALESCE(docker_manifest_path, ''),
		       COALESCE(output_dir, ''), COALESCE(model_summary, ''), COALESCE(language_summary, ''),
		       sample_count, accepted_count, success_count, failure_count, total_tokens,
		       COALESCE(prompt_strategy, ''), COALESCE(prompt_version_id, ''), created_at_utc, updated_at_utc
		FROM generated_sets
		WHERE generated_set_id = ?`, id)
	return scanGeneratedSet(row)
}

type generatedSetScanner interface {
	Scan(dest ...any) error
}

func scanGeneratedSet(scanner generatedSetScanner) (GeneratedSetItem, error) {
	var item GeneratedSetItem
	var totalTokens sql.NullInt64
	if err := scanner.Scan(&item.GeneratedSetID, &item.Name, &item.Note, &item.Status, &item.SourceRunID,
		&item.SourceManifestPath, &item.ManifestPath, &item.DockerManifestPath, &item.OutputDir,
		&item.ModelSummary, &item.LanguageSummary, &item.SampleCount, &item.AcceptedCount, &item.SuccessCount, &item.FailureCount,
		&totalTokens, &item.PromptStrategy, &item.PromptVersionID, &item.CreatedAtUTC, &item.UpdatedAtUTC); err != nil {
		return GeneratedSetItem{}, err
	}
	item.TotalTokens = nullableSQLInt(totalTokens)
	return item, nil
}

func (s *SQLiteStore) ListGeneratedSetSamples(ctx context.Context, id string, limit int) ([]GeneratedSetSampleItem, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT generated_set_id, generated_case_id, run_id, model, language, sample_id,
		       COALESCE(sample_uid, ''), COALESCE(sample_path, ''), COALESCE(generated_test_path, ''),
		       COALESCE(generated_test_artifact_id, ''), COALESCE(generated_test_sha256, ''),
		       success, status, COALESCE(note, ''), COALESCE(tags_json, ''), created_at_utc, updated_at_utc
		FROM generated_set_samples
		WHERE generated_set_id = ?
		ORDER BY language, model, sample_id
		LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GeneratedSetSampleItem{}
	for rows.Next() {
		var item GeneratedSetSampleItem
		var success int
		if err := rows.Scan(&item.GeneratedSetID, &item.GeneratedCaseID, &item.RunID, &item.Model, &item.Language, &item.SampleID,
			&item.SampleUID, &item.SamplePath, &item.GeneratedTestPath, &item.GeneratedTestArtifactID, &item.GeneratedTestSHA256,
			&success, &item.Status, &item.Note, &item.TagsJSON, &item.CreatedAtUTC, &item.UpdatedAtUTC); err != nil {
			return nil, err
		}
		item.Success = success != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateGeneratedSet(ctx context.Context, id string, opts UpdateGeneratedSetOptions) (GeneratedSetItem, error) {
	current, err := s.GetGeneratedSet(ctx, id)
	if err != nil {
		return GeneratedSetItem{}, err
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = current.Name
	}
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = current.Status
	}
	manifestPath := firstNonEmpty(opts.ManifestPath, current.ManifestPath)
	dockerManifestPath := firstNonEmpty(opts.DockerManifestPath, current.DockerManifestPath)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE generated_sets
		SET name=?, note=?, status=?, manifest_path=?, docker_manifest_path=?, updated_at_utc=?
		WHERE generated_set_id=?`,
		name, nullString(opts.Note), status, nullString(manifestPath), nullString(dockerManifestPath), nowUTC(), id); err != nil {
		return GeneratedSetItem{}, err
	}
	return s.GetGeneratedSet(ctx, id)
}

func (s *SQLiteStore) UpdateGeneratedSetSample(ctx context.Context, setID, caseID string, opts UpdateGeneratedSetSampleOptions) (GeneratedSetSampleItem, error) {
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "accepted"
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE generated_set_samples
		SET status=?, note=?, tags_json=?, updated_at_utc=?
		WHERE generated_set_id=? AND generated_case_id=?`,
		status, nullString(opts.Note), nullString(opts.TagsJSON), nowUTC(), setID, caseID); err != nil {
		return GeneratedSetSampleItem{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT generated_set_id, generated_case_id, run_id, model, language, sample_id,
		       COALESCE(sample_uid, ''), COALESCE(sample_path, ''), COALESCE(generated_test_path, ''),
		       COALESCE(generated_test_artifact_id, ''), COALESCE(generated_test_sha256, ''),
		       success, status, COALESCE(note, ''), COALESCE(tags_json, ''), created_at_utc, updated_at_utc
		FROM generated_set_samples
		WHERE generated_set_id = ? AND generated_case_id = ?`, setID, caseID)
	if err != nil {
		return GeneratedSetSampleItem{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return GeneratedSetSampleItem{}, sql.ErrNoRows
	}
	var item GeneratedSetSampleItem
	var success int
	if err := rows.Scan(&item.GeneratedSetID, &item.GeneratedCaseID, &item.RunID, &item.Model, &item.Language, &item.SampleID,
		&item.SampleUID, &item.SamplePath, &item.GeneratedTestPath, &item.GeneratedTestArtifactID, &item.GeneratedTestSHA256,
		&success, &item.Status, &item.Note, &item.TagsJSON, &item.CreatedAtUTC, &item.UpdatedAtUTC); err != nil {
		return GeneratedSetSampleItem{}, err
	}
	item.Success = success != 0
	return item, rows.Err()
}
