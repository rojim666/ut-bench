package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"go-ut-bench/internal/contracts"
	"go-ut-bench/internal/orchestrator"
)

// DockerConfig captures the static inputs required to execute a run inside
// the utbench container image on the local docker daemon.
type DockerConfig struct {
	EvalImageName string // e.g. "utbench:latest"
	ProjectRoot   string // host absolute path of project root (parent of datasets/, artifacts/, configs/)
	EnvFile       string // optional host path to .env; ignored if empty or missing
	EvalMemory    string // optional Docker memory limit for eval containers, e.g. "4g"
	EvalCPUs      string // optional Docker CPU limit for eval containers, e.g. "4"
}

const containerDefaultDBPath = "/tmp/utbench.db"

func (c DockerConfig) EffectiveEvalImage() string {
	if strings.TrimSpace(c.EvalImageName) != "" {
		return strings.TrimSpace(c.EvalImageName)
	}
	return "utbench:latest"
}

// runInDocker shells out to `docker run ...` and streams the combined output
// line-by-line into entry's log buffer. The host directories datasets/,
// artifacts/, configs/ and storage/ are mounted so the in-container CLI writes
// artifacts back to the host the same way the in-process runner does.
//
// On success, the container has already produced run_summary.json etc. under
// artifacts/runs/<run-id>/ so the existing listRuns/handleRunReport code keeps
// working unchanged.
func runInDocker(ctx context.Context, entry *RunEntry, spec contracts.RunSpec, opts orchestrator.Options, cfg DockerConfig) error {
	args := buildDockerRunArgs(spec, opts, cfg)
	// 注入稳定容器名，使 docker pause/unpause/kill 可以定位到本次运行。
	// `--name` 必须紧跟在 `docker run` 之后、镜像名之前。
	if entry.container != "" {
		args = append([]string{args[0], "--name", entry.container}, args[1:]...)
	}
	entry.appendLog(fmt.Sprintf("[%s] docker exec → docker %s", logTS(), redactArgs(args)))

	cmd := exec.CommandContext(ctx, "docker", args...)
	hideCommandWindow(cmd)
	// Windows Git Bash 的 MSYS 会把 -e 传的路径值自动转换（如 /c/Users → C:/Program Files/Git/c/Users），
	// 导致容器内环境变量损坏。设置 MSYS_NO_PATHCONV=1 禁止此转换。
	cmd.Env = append(os.Environ(), "MSYS_NO_PATHCONV=1")
	// Merge stdout + stderr into the same line-sink so users see everything
	// (build messages, evaluator logs, mutation output) in order.
	// 同时用 tailWriter 保留最后 N 行，失败时回填到 error message 里，
	// 避免 "exit status 125" 这种无信息错误。
	tail := &tailWriter{max: 20}
	lw := &lineWriter{run: entry}
	cmd.Stdout = io.MultiWriter(lw, tail)
	cmd.Stderr = io.MultiWriter(lw, tail)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("docker run start: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		if t := strings.TrimSpace(tail.String()); t != "" {
			return fmt.Errorf("docker run failed: %w; last output:\n%s", err, t)
		}
		return fmt.Errorf("docker run failed: %w", err)
	}
	return nil
}

// tailWriter 仅保留最后 max 行用于错误诊断。
type tailWriter struct {
	max  int
	buf  []string
	rest string
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.rest += string(p)
	for {
		idx := strings.IndexByte(t.rest, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(t.rest[:idx], "\r")
		t.rest = t.rest[idx+1:]
		t.buf = append(t.buf, line)
		if len(t.buf) > t.max {
			t.buf = t.buf[len(t.buf)-t.max:]
		}
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	out := append([]string(nil), t.buf...)
	if rest := strings.TrimRight(t.rest, "\r"); rest != "" {
		out = append(out, rest)
	}
	return strings.Join(out, "\n")
}

// buildDockerRunArgs assembles the argv for `docker run`.
// It deliberately mirrors the flag set that `utbench run` understands, so
// behaviour matches the in-process backend one-to-one.
// When opts.Phase is set to "generate", "evaluate", or "report", the command
// switches to the corresponding CLI subcommand instead of "run".
func buildDockerRunArgs(spec contracts.RunSpec, opts orchestrator.Options, cfg DockerConfig) []string {
	a := []string{"run", "--rm"}

	if cfg.EnvFile != "" {
		a = append(a, "--env-file", cfg.EnvFile)
	}
	if memory := strings.TrimSpace(cfg.EvalMemory); memory != "" {
		a = append(a, "--memory", memory, "--memory-swap", memory)
	}
	if cpus := strings.TrimSpace(cfg.EvalCPUs); cpus != "" {
		a = append(a, "--cpus", cpus)
	}

	// Mounts: datasets (read-only is safer but writable matches current UX),
	// artifacts, configs, storage. Paths on the container side are fixed and
	// mirror those used in STARTUP_GUIDE.md.
	root := strings.TrimRight(cfg.ProjectRoot, `/\`)
	datasetRoot := resolveDockerHostPath(root, spec.DatasetRoot, "datasets")
	dbPath := dockerDBPath(root, spec, opts)
	a = append(a,
		"-e", "UTBENCH_SANDBOX_HOST_PROJECT_ROOT="+filepath.ToSlash(root),
		"-e", "UTBENCH_SANDBOX_CONTAINER_PROJECT_ROOT=/app",
		"-e", "UTBENCH_SANDBOX_HOST_OUTPUT_ROOT="+filepath.ToSlash(filepath.Join(root, "artifacts")),
		"-e", "UTBENCH_SANDBOX_CONTAINER_OUTPUT_ROOT=/app/artifacts",
		"-e", "UTBENCH_SANDBOX_HOST_DATASET_ROOT="+filepath.ToSlash(datasetRoot),
		"-e", "UTBENCH_SANDBOX_CONTAINER_DATASET_ROOT=/app/datasets",
		"-e", "UTBENCH_DB_PATH="+dbPath,
		"-v", datasetRoot+`:/app/datasets`,
		"-v", root+`/artifacts:/app/artifacts`,
		"-v", root+`/configs:/app/configs`,
		"-v", root+`/storage:/app/storage`,
		// 持久化 Maven 本地仓库，避免每次容器运行都重新下载依赖。
		// Docker 镜像已预下载关键依赖，但此 mount 可缓存运行时新增的依赖。
		"-v", root+`/.m2-cache:/root/.m2/repository`,
	)
	a = append(a, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	// 统一容器内可能还会启动 CLI Agent 沙箱；挂载 docker.sock 后由内层
	// utbench 通过 Docker-outside-of-Docker 启动这些子容器。

	// Determine the CLI subcommand based on phase.
	// Phase "full" (or empty) uses "run" command.
	// Phase "generate", "evaluate", "report" uses the corresponding subcommand.
	phase := opts.Phase
	if phase == "" {
		phase = "full"
	}
	cliCmd := "run"
	switch phase {
	case "generate":
		cliCmd = "generate"
	case "evaluate":
		cliCmd = "evaluate"
	case "report":
		cliCmd = "report"
	}

	a = append(a, cfg.EffectiveEvalImage(), cliCmd)

	// Common flags for all commands
	a = append(a,
		"--run-id", spec.RunID,
		"--output-root", "/app/artifacts",
	)

	// Determine source run ID for artifact paths
	sourceRunID := opts.SourceRunID
	if sourceRunID == "" {
		sourceRunID = spec.RunID
	}

	// Phase-specific flags
	switch cliCmd {
	case "run":
		a = append(a,
			"--models", strings.Join(spec.Models, ","),
			"--langs", strings.Join(spec.Languages, ","),
			"--dataset-root", "/app/datasets",
			"--config", "/app/configs/models.yaml",
		)
		if spec.AgentsConfigPath != "" {
			a = append(a, "--agents-config", path.Join("/app/configs", filepath.Base(spec.AgentsConfigPath)))
		}
		if len(spec.Subjects) > 0 {
			a = append(a, "--subjects", strings.Join(spec.Subjects, ","))
		}
		if len(spec.DatasetClasses) > 0 {
			a = append(a, "--class", strings.Join(spec.DatasetClasses, ","))
		}
		if spec.DatasetManifest != "" {
			a = append(a, "--dataset-manifest", dockerConfigPath(spec.DatasetManifest))
		}
		if spec.DatasetScenario != "" {
			a = append(a, "--scenario", spec.DatasetScenario)
		}
		if spec.DatasetProject != "" {
			a = append(a, "--project", spec.DatasetProject)
		}
		if spec.DatasetLevel != "" {
			a = append(a, "--level", spec.DatasetLevel)
		}
		if spec.MaxSamples > 0 {
			a = append(a, "--max-samples", fmt.Sprintf("%d", spec.MaxSamples))
		}
		if spec.Workers > 0 {
			a = append(a, "--workers", fmt.Sprintf("%d", spec.Workers))
		}
		if spec.Mode != "" {
			a = append(a, "--mode", string(spec.Mode))
		}
		if spec.DryRun {
			a = append(a, "--dry-run")
		}
		needsDBPath := false
		if spec.ReuseGenerated {
			a = append(a, "--reuse-generated")
			needsDBPath = true
		}
		if spec.ReuseEvaluation {
			a = append(a, "--reuse-evaluation")
			needsDBPath = true
		}
		a = append(a, fmt.Sprintf("--mutation-enabled=%t", spec.MutationEnabled))
		if spec.MutationTimeout > 0 {
			a = append(a, "--mutation-timeout", fmt.Sprintf("%d", spec.MutationTimeout))
		}
		if spec.MutationPolicy != "" {
			a = append(a, "--mutation-policy", spec.MutationPolicy)
		}
		if spec.TestTimeout > 0 {
			a = append(a, "--test-timeout", fmt.Sprintf("%d", spec.TestTimeout))
		}
		if opts.Ingest {
			a = append(a, "--ingest")
			needsDBPath = true
		}
		if opts.StrictIngest {
			a = append(a, "--strict-ingest")
		}
		if needsDBPath {
			a = append(a, "--db-path", dbPath)
		}

	case "generate":
		a = append(a,
			"--models", strings.Join(spec.Models, ","),
			"--langs", strings.Join(spec.Languages, ","),
			"--dataset-root", "/app/datasets",
			"--config", "/app/configs/models.yaml",
		)
		if spec.AgentsConfigPath != "" {
			a = append(a, "--agents-config", path.Join("/app/configs", filepath.Base(spec.AgentsConfigPath)))
		}
		if len(spec.Subjects) > 0 {
			a = append(a, "--subjects", strings.Join(spec.Subjects, ","))
		}
		if len(spec.DatasetClasses) > 0 {
			a = append(a, "--class", strings.Join(spec.DatasetClasses, ","))
		}
		if spec.DatasetManifest != "" {
			a = append(a, "--dataset-manifest", dockerConfigPath(spec.DatasetManifest))
		}
		if spec.DatasetScenario != "" {
			a = append(a, "--scenario", spec.DatasetScenario)
		}
		if spec.DatasetProject != "" {
			a = append(a, "--project", spec.DatasetProject)
		}
		if spec.DatasetLevel != "" {
			a = append(a, "--level", spec.DatasetLevel)
		}
		if spec.MaxSamples > 0 {
			a = append(a, "--max-samples", fmt.Sprintf("%d", spec.MaxSamples))
		}
		if spec.Workers > 0 {
			a = append(a, "--workers", fmt.Sprintf("%d", spec.Workers))
		}
		if spec.Mode != "" {
			a = append(a, "--mode", string(spec.Mode))
		}
		if spec.DryRun {
			a = append(a, "--dry-run")
		}
		if spec.ReuseGenerated {
			a = append(a, "--reuse-generated", "--db-path", dbPath)
		}

	case "evaluate":
		// Use explicit manifest path if provided, otherwise use source run's manifest
		manifestPath := opts.ManifestPath
		if manifestPath == "" {
			manifestPath = path.Join("/app/artifacts", "runs", sourceRunID, "generated", "generated_manifest.json")
		} else {
			manifestPath = dockerContainerPathForMountedFile(root, manifestPath)
		}
		a = append(a, "--manifest", manifestPath)
		a = append(a, fmt.Sprintf("--mutation-enabled=%t", spec.MutationEnabled))
		if spec.MutationTimeout > 0 {
			a = append(a, "--mutation-timeout", fmt.Sprintf("%d", spec.MutationTimeout))
		}
		if spec.MutationPolicy != "" {
			a = append(a, "--mutation-policy", spec.MutationPolicy)
		}
		if spec.TestTimeout > 0 {
			a = append(a, "--test-timeout", fmt.Sprintf("%d", spec.TestTimeout))
		}
		if spec.ReuseEvaluation {
			a = append(a, "--reuse-evaluation", "--db-path", dbPath)
		}

	case "report":
		// Use explicit evaluation path if provided, otherwise use source run's evaluation
		evaluationPath := opts.EvaluationPath
		if evaluationPath == "" {
			evaluationPath = path.Join("/app/artifacts", "runs", sourceRunID, "evaluation", "evaluation_result.json")
		} else {
			evaluationPath = dockerContainerPathForMountedFile(root, evaluationPath)
		}
		a = append(a, "--evaluation", evaluationPath)
	}

	return wrapDockerSourceCommand(a, cfg)
}

func dockerDBPath(projectRoot string, spec contracts.RunSpec, opts orchestrator.Options) string {
	if v := strings.TrimSpace(os.Getenv("UTBENCH_DB_PATH")); v != "" {
		if isContainerDBPath(v) {
			return path.Clean(filepath.ToSlash(v))
		}
		return dockerContainerPathForMountedFile(projectRoot, v)
	}
	raw := strings.TrimSpace(opts.DBPath)
	if raw == "" {
		raw = strings.TrimSpace(spec.DBPath)
	}
	if isDefaultDockerHostDBPath(projectRoot, raw) {
		return containerDefaultDBPath
	}
	if isContainerDBPath(raw) {
		return path.Clean(filepath.ToSlash(raw))
	}
	return dockerContainerPathForMountedFile(projectRoot, raw)
}

func isContainerDBPath(dbPath string) bool {
	slash := filepath.ToSlash(strings.TrimSpace(dbPath))
	return strings.HasPrefix(slash, "/app/") || strings.HasPrefix(slash, "/tmp/")
}

func isDefaultDockerHostDBPath(projectRoot, dbPath string) bool {
	raw := strings.TrimSpace(dbPath)
	if raw == "" {
		return true
	}
	slashRaw := filepath.ToSlash(raw)
	if slashRaw == "/app/storage/utbench.db" ||
		slashRaw == "./storage/utbench.db" ||
		slashRaw == "storage/utbench.db" {
		return true
	}
	root := strings.TrimRight(filepath.ToSlash(filepath.Clean(projectRoot)), "/")
	clean := filepath.Clean(raw)
	if !filepath.IsAbs(clean) {
		clean = filepath.Join(projectRoot, clean)
	}
	slash := filepath.ToSlash(filepath.Clean(clean))
	return slash == path.Join(root, "storage", "utbench.db")
}

func dockerContainerPathForMountedFile(projectRoot, filePath string) string {
	raw := strings.TrimSpace(filePath)
	if raw == "" {
		return filePath
	}
	slashRaw := filepath.ToSlash(raw)
	if strings.HasPrefix(slashRaw, "/app/") {
		return path.Clean(slashRaw)
	}

	root := strings.TrimRight(filepath.ToSlash(filepath.Clean(projectRoot)), "/")
	cleanSlashRaw := path.Clean(slashRaw)
	if cleanSlashRaw == root {
		return "/app"
	}
	if strings.HasPrefix(cleanSlashRaw, root+"/") {
		return path.Join("/app", cleanSlashRaw[len(root)+1:])
	}

	clean := filepath.Clean(raw)
	if !filepath.IsAbs(clean) {
		clean = filepath.Join(projectRoot, clean)
	}
	slash := filepath.ToSlash(filepath.Clean(clean))
	if slash == root {
		return "/app"
	}
	if strings.HasPrefix(slash, root+"/") {
		return path.Join("/app", slash[len(root)+1:])
	}
	return slashRaw
}

func dockerConfigPath(filePath string) string {
	raw := strings.TrimSpace(filePath)
	if raw == "" {
		return raw
	}
	slash := filepath.ToSlash(raw)
	if strings.HasPrefix(slash, "/app/") {
		return path.Clean(slash)
	}
	if strings.HasPrefix(slash, "configs/") {
		return path.Join("/app", slash)
	}
	return path.Join("/app/configs", filepath.Base(raw))
}

func resolveDockerHostPath(projectRoot, requested, fallbackName string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return strings.TrimRight(projectRoot, `/\`) + `/` + fallbackName
	}
	requested = filepath.Clean(requested)
	if filepath.IsAbs(requested) {
		return filepath.ToSlash(requested)
	}
	return filepath.ToSlash(filepath.Clean(filepath.Join(projectRoot, requested)))
}

// runEvaluateInDocker runs only the evaluation step inside the utbench container.
// Unlike runInDocker (which runs the full pipeline), this is a simpler synchronous
// wrapper that captures output as a string. Used by the reevaluate API handler.
func runEvaluateInDocker(ctx context.Context, runID string, spec contracts.RunSpec, manifestPath string, cfg DockerConfig) (string, error) {
	opts := orchestrator.Options{
		Phase:        "evaluate",
		SourceRunID:  runID,
		ManifestPath: manifestPath,
	}
	args := buildDockerRunArgs(spec, opts, cfg)
	cmd := exec.CommandContext(ctx, "docker", args...)
	hideCommandWindow(cmd)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func wrapDockerSourceCommand(args []string, cfg DockerConfig) []string {
	imageIdx := -1
	for i, arg := range args {
		if arg == cfg.EffectiveEvalImage() {
			imageIdx = i
			break
		}
	}
	if imageIdx < 0 || imageIdx == len(args)-1 {
		return args
	}
	// Dockerfile ENTRYPOINT 使用 /app/utbench，命令从子命令开始即可。
	out := append([]string{}, args[:imageIdx]...)
	out = append(out, cfg.EffectiveEvalImage())
	out = append(out, args[imageIdx+1:]...)
	return out
}

func buildDockerBaseArgs(cfg DockerConfig) []string {
	a := []string{"run", "--rm"}
	if cfg.EnvFile != "" && fileExists(cfg.EnvFile) {
		a = append(a, "--env-file", cfg.EnvFile)
	}
	if memory := strings.TrimSpace(cfg.EvalMemory); memory != "" {
		a = append(a, "--memory", memory, "--memory-swap", memory)
	}
	if cpus := strings.TrimSpace(cfg.EvalCPUs); cpus != "" {
		a = append(a, "--cpus", cpus)
	}
	root := strings.TrimRight(cfg.ProjectRoot, `/\`)
	return append(a,
		"-v", root+`/datasets:/app/datasets`,
		"-v", root+`/artifacts:/app/artifacts`,
		"-v", root+`/configs:/app/configs`,
		"-v", root+`/storage:/app/storage`,
	)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func defaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// redactArgs produces a single-line human-readable representation of the argv
// without leaking any secrets. Currently we simply join with spaces since the
// arguments don't contain credentials (API keys come from --env-file).
func redactArgs(args []string) string {
	return strings.Join(args, " ")
}
