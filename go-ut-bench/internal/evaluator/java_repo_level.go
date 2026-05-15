package evaluator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func prepareJavaRepoLevelWorkspace(testPath, samplePath string) (workdir, testRel, moduleDir, className, testClassName, errMsg string) {
	meta := loadRepoLevelMeta(samplePath)
	if meta == nil {
		return "", "", "", "", "", "repo_level sample missing metadata"
	}
	if strings.TrimSpace(meta.WorkspaceRoot) == "" {
		return "", "", "", "", "", "repo_level workspace_root not set in metadata"
	}
	if strings.TrimSpace(meta.TargetFile) == "" {
		return "", "", "", "", "", "repo_level target_file not set in metadata"
	}
	if _, err := os.Stat(meta.WorkspaceRoot); err != nil {
		return "", "", "", "", "", "repo_level workspace not found: " + meta.WorkspaceRoot
	}

	testSource, err := os.ReadFile(testPath)
	if err != nil {
		return "", "", "", "", "", fmt.Sprintf("failed to read generated test: %s", err)
	}
	targetAbs := filepath.Join(meta.WorkspaceRoot, filepath.FromSlash(meta.TargetFile))
	sourceData, err := os.ReadFile(targetAbs)
	if err != nil {
		return "", "", "", "", "", fmt.Sprintf("failed to read source: %s", err)
	}
	classNames := extractAllClassNamesFromSource(string(sourceData))
	if len(classNames) == 0 {
		classNames = []string{strings.TrimSuffix(filepath.Base(meta.TargetFile), filepath.Ext(meta.TargetFile))}
	}
	className = classNames[0]
	testClassName = extractTestClassNameFromTest(string(testSource))
	if testClassName == "" {
		testClassName = className + "Test"
	}
	testSource = normalizeJavaGeneratedTestPackage(testSource, meta.PackageName)

	tmpdir, err := os.MkdirTemp("", "utbench_java_repo_eval_")
	if err != nil {
		return "", "", "", "", "", fmt.Sprintf("failed to create temp dir: %s", err)
	}

	skip := func(rel string) bool {
		relSlash := filepath.ToSlash(rel)
		base := filepath.Base(relSlash)
		if base == ".git" || base == "target" || base == "build" || base == "out" || base == ".gradle" || base == ".idea" {
			return true
		}
		if strings.HasSuffix(base, ".meta.json") {
			return true
		}
		if relSlash == "src/test" || strings.HasPrefix(relSlash, "src/test/") || strings.Contains(relSlash, "/src/test/") {
			return true
		}
		return false
	}
	if err := copyTreeFiltered(meta.WorkspaceRoot, tmpdir, skip); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to copy workspace: %s", err)
	}

	if _, err := os.Stat(filepath.Join(tmpdir, filepath.FromSlash(meta.TargetFile))); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("target_file not found in workspace: %s", meta.TargetFile)
	}

	moduleDir = javaModuleDirForTarget(meta.WorkspaceRoot, meta.TargetFile)
	testRel = javaTestRelForTarget(meta.TargetFile, meta.PackageName, testClassName)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(tmpdir, filepath.FromSlash(testRel))), 0o755); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to create test dir: %s", err)
	}
	if err := os.WriteFile(filepath.Join(tmpdir, filepath.FromSlash(testRel)), testSource, 0o644); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to write test: %s", err)
	}
	return tmpdir, testRel, moduleDir, className, testClassName, ""
}

func normalizeJavaGeneratedTestPackage(source []byte, wantPackage string) []byte {
	wantPackage = strings.TrimSpace(wantPackage)
	re := regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;\s*`)
	loc := re.FindIndex(source)
	if wantPackage == "" {
		if loc == nil {
			return source
		}
		out := make([]byte, 0, len(source)-(loc[1]-loc[0]))
		out = append(out, source[:loc[0]]...)
		out = append(out, source[loc[1]:]...)
		return out
	}
	decl := []byte("package " + wantPackage + ";\n\n")
	if loc == nil {
		out := make([]byte, 0, len(source)+len(decl))
		out = append(out, decl...)
		out = append(out, source...)
		return out
	}
	out := make([]byte, 0, len(source)-(loc[1]-loc[0])+len(decl))
	out = append(out, source[:loc[0]]...)
	out = append(out, decl...)
	out = append(out, source[loc[1]:]...)
	return out
}

func javaTestRelForTarget(targetFile, packageName, testClassName string) string {
	targetFile = filepath.ToSlash(targetFile)
	marker := "/src/main/java/"
	if strings.HasPrefix(targetFile, "src/main/java/") {
		marker = "src/main/java/"
	}
	if idx := strings.Index(targetFile, marker); idx >= 0 {
		prefix := targetFile[:idx]
		if strings.HasSuffix(prefix, "/") {
			prefix = strings.TrimSuffix(prefix, "/")
		}
		pkgPath := strings.ReplaceAll(strings.TrimSpace(packageName), ".", "/")
		parts := []string{}
		if prefix != "" {
			parts = append(parts, prefix)
		}
		parts = append(parts, "src", "test", "java")
		if pkgPath != "" {
			parts = append(parts, pkgPath)
		}
		parts = append(parts, testClassName+".java")
		return filepath.ToSlash(filepath.Join(parts...))
	}
	return filepath.ToSlash(filepath.Join(filepath.Dir(targetFile), testClassName+".java"))
}

func javaModuleDirForTarget(workspaceRoot, targetFile string) string {
	targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(targetFile))
	moduleRoot, ok := findNearestJavaBuildRoot(targetAbs)
	if !ok {
		return "."
	}
	rel, err := filepath.Rel(workspaceRoot, moduleRoot)
	if err != nil || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return "."
	}
	return filepath.ToSlash(rel)
}

func findNearestJavaBuildRoot(path string) (string, bool) {
	markers := []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}
	dir := filepath.Dir(path)
	for {
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func javaMavenArgsForModule(moduleDir string, args ...string) []string {
	out := []string{"-q"}
	moduleDir = strings.TrimSpace(filepath.ToSlash(moduleDir))
	if moduleDir != "" && moduleDir != "." {
		out = append(out, "-pl", moduleDir, "-am")
	}
	out = append(out, args...)
	return out
}

func javaCompileCheckRepoLevel(workdir, moduleDir string) (bool, string) {
	compileTimeout := defaultTestTimeoutSeconds * 3
	runCtx, cancel := context.WithTimeout(context.Background(), time.Duration(compileTimeout)*time.Second)
	defer cancel()
	args := javaMavenArgsForModule(moduleDir, "-DskipTests", "test-compile")
	output, err := runCommandWithProcessGroupKill(runCtx, "mvn", args, workdir, nil)
	if runCtx.Err() != nil {
		return false, fmt.Sprintf("java compile timed out after %ds", compileTimeout)
	}
	if err != nil {
		return false, trimErr(string(output), 3000)
	}
	return true, ""
}

func executeJavaTestsRepoLevel(workdir, moduleDir, testClassName string, timeoutSeconds int) (bool, string, int) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultTestTimeoutSeconds
	}
	if timeoutSeconds < 300 {
		timeoutSeconds = 300
	}
	started := time.Now()
	runCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	args := javaMavenArgsForModule(moduleDir, "-Dtest="+testClassName, "-DfailIfNoTests=false", "test")
	output, err := runCommandWithProcessGroupKill(runCtx, "mvn", args, workdir, nil)
	latency := int(time.Since(started).Milliseconds())
	if runCtx.Err() != nil {
		return false, fmt.Sprintf("java test timed out after %ds", timeoutSeconds), latency
	}
	if err == nil {
		return true, string(output), latency
	}
	return false, trimErr(string(output), 4000), latency
}

func collectJavaCoverageRepoLevel(workdir, moduleDir, className, testClassName string, timeoutSeconds int) (float64, float64, string) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultTestTimeoutSeconds
	}
	if timeoutSeconds < 300 {
		timeoutSeconds = 300
	}
	runCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	args := javaMavenArgsForModule(moduleDir,
		"-Dtest="+testClassName,
		"-DfailIfNoTests=false",
		"org.jacoco:jacoco-maven-plugin:prepare-agent",
		"test",
		"org.jacoco:jacoco-maven-plugin:report",
	)
	if out, err := runCommandWithProcessGroupKill(runCtx, "mvn", args, workdir, nil); err != nil {
		if runCtx.Err() != nil {
			return 0, 0, fmt.Sprintf("java coverage timed out after %ds", timeoutSeconds)
		}
		return 0, 0, "jacoco run failed: " + trimErr(string(out), 1200)
	}
	jacocoXML := filepath.Join(workdir, "target", "site", "jacoco", "jacoco.xml")
	if strings.TrimSpace(moduleDir) != "" && moduleDir != "." {
		jacocoXML = filepath.Join(workdir, filepath.FromSlash(moduleDir), "target", "site", "jacoco", "jacoco.xml")
	}
	raw, err := os.ReadFile(jacocoXML)
	if err != nil {
		return 0, 0, "jacoco.xml not found"
	}
	return parseJacocoXML(string(raw), className)
}
