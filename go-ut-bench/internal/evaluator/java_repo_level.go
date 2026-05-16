package evaluator

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const javaRepoLevelJUnitJupiterDependency = `    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter</artifactId>
      <version>5.10.2</version>
      <scope>test</scope>
    </dependency>
`

const javaRepoLevelSurefirePlugin = `      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-surefire-plugin</artifactId>
        <version>3.2.5</version>
        <configuration>
          <failIfNoSpecifiedTests>false</failIfNoSpecifiedTests>
          <failIfNoTests>false</failIfNoTests>
        </configuration>
      </plugin>
`

const javaRepoLevelPitestPlugin = `      <plugin>
        <groupId>org.pitest</groupId>
        <artifactId>pitest-maven</artifactId>
        <version>1.19.6</version>
        <dependencies>
          <dependency>
            <groupId>org.pitest</groupId>
            <artifactId>pitest-junit5-plugin</artifactId>
            <version>1.2.1</version>
          </dependency>
        </dependencies>
        <configuration>
          <outputFormats>
            <param>XML</param>
            <param>CSV</param>
          </outputFormats>
          <timestampedReports>false</timestampedReports>
          <failWhenNoMutations>false</failWhenNoMutations>
          <mutationThreshold>0</mutationThreshold>
          <coverageThreshold>0</coverageThreshold>
          <threads>1</threads>
        </configuration>
      </plugin>
`

var javaMavenMu sync.Mutex

var (
	javaRepoMainSourceRootRe = regexp.MustCompile(`(^|/)src/main/java[^/]*/`)
	javaRepoTestSourceRootRe = regexp.MustCompile(`(^|/)src/test/java[^/]*/`)
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
		if isJavaRepoExistingTestCase(relSlash) {
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
	if err := prepareJavaRepoLevelPOMs(tmpdir, moduleDir); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", "", "", "", "", fmt.Sprintf("failed to prepare Maven POMs: %s", err)
	}
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

func isJavaRepoExistingTestCase(rel string) bool {
	rel = strings.ToLower(filepath.ToSlash(rel))
	if !strings.HasSuffix(rel, ".java") {
		return false
	}
	return javaRepoTestSourceRootRe.MatchString(rel)
}

func prepareJavaRepoLevelPOMs(workdir, moduleDir string) error {
	if err := filepath.WalkDir(workdir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "target" || base == "build" || base == "out" || base == ".gradle" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "pom.xml" {
			return nil
		}
		return rewriteJavaRepoLevelPOM(path, false)
	}); err != nil {
		return err
	}
	modulePom := filepath.Join(workdir, filepath.FromSlash(moduleDir), "pom.xml")
	if strings.TrimSpace(moduleDir) == "" || moduleDir == "." {
		modulePom = filepath.Join(workdir, "pom.xml")
	}
	if _, err := os.Stat(modulePom); err != nil {
		return fmt.Errorf("module pom not found: %s", modulePom)
	}
	return rewriteJavaRepoLevelPOM(modulePom, true)
}

func rewriteJavaRepoLevelPOM(path string, ensureJUnit bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated := disableCentralPublishingExtension(string(raw))
	updated = relaxJavaRepoLevelStrictWarnings(updated)
	for _, artifactID := range javaRepoLevelQualityGatePlugins {
		updated = disableJavaRepoLevelPlugin(updated, artifactID)
	}
	if ensureJUnit {
		updated = ensureJUnitJupiterDependency(updated)
		updated = ensureJavaRepoLevelBuildPlugin(updated, "maven-surefire-plugin", javaRepoLevelSurefirePlugin)
		updated = ensureJavaRepoLevelBuildPlugin(updated, "pitest-maven", javaRepoLevelPitestPlugin)
	}
	if updated == string(raw) {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

var javaRepoLevelQualityGatePlugins = []string{
	"apache-rat-plugin",
	"animal-sniffer-maven-plugin",
	"forbiddenapis",
	"japicmp-maven-plugin",
	"maven-checkstyle-plugin",
	"maven-enforcer-plugin",
	"maven-pmd-plugin",
	"modernizer-maven-plugin",
	"palantir-java-format-maven-plugin",
	"proguard-maven-plugin",
	"revapi-maven-plugin",
	"spotbugs-maven-plugin",
	"spotless-maven-plugin",
}

func disableCentralPublishingExtension(pom string) string {
	const pluginStart = "<plugin>"
	const pluginEnd = "</plugin>"
	artifactRe := regexp.MustCompile(`(?s)<artifactId>\s*central-publishing-maven-plugin\s*</artifactId>`)
	extensionRe := regexp.MustCompile(`(?s)<extensions>\s*true\s*</extensions>`)
	var b strings.Builder
	cursor := 0
	for {
		startRel := strings.Index(pom[cursor:], pluginStart)
		if startRel < 0 {
			b.WriteString(pom[cursor:])
			break
		}
		start := cursor + startRel
		endRel := strings.Index(pom[start:], pluginEnd)
		if endRel < 0 {
			b.WriteString(pom[cursor:])
			break
		}
		end := start + endRel + len(pluginEnd)
		b.WriteString(pom[cursor:start])
		block := pom[start:end]
		if artifactRe.MatchString(block) {
			block = extensionRe.ReplaceAllString(block, "<extensions>false</extensions>")
		}
		b.WriteString(block)
		cursor = end
	}
	return b.String()
}

func relaxJavaRepoLevelStrictWarnings(pom string) string {
	failOnWarningRe := regexp.MustCompile(`(?s)<failOnWarning>\s*true\s*</failOnWarning>`)
	failOnWarningsRe := regexp.MustCompile(`(?s)<failOnWarnings>\s*true\s*</failOnWarnings>`)
	pom = failOnWarningRe.ReplaceAllString(pom, "<failOnWarning>false</failOnWarning>")
	pom = failOnWarningsRe.ReplaceAllString(pom, "<failOnWarnings>false</failOnWarnings>")
	return pom
}

func disableJavaRepoLevelPlugin(pom, artifactID string) string {
	const pluginStart = "<plugin>"
	const pluginEnd = "</plugin>"
	artifactRe := regexp.MustCompile(`(?s)<artifactId>\s*` + regexp.QuoteMeta(artifactID) + `\s*</artifactId>`)
	var b strings.Builder
	cursor := 0
	for {
		startRel := strings.Index(pom[cursor:], pluginStart)
		if startRel < 0 {
			b.WriteString(pom[cursor:])
			break
		}
		start := cursor + startRel
		endRel := strings.Index(pom[start:], pluginEnd)
		if endRel < 0 {
			b.WriteString(pom[cursor:])
			break
		}
		end := start + endRel + len(pluginEnd)
		b.WriteString(pom[cursor:start])
		block := pom[start:end]
		if !artifactRe.MatchString(block) {
			b.WriteString(block)
		}
		cursor = end
	}
	return b.String()
}

func ensureJUnitJupiterDependency(pom string) string {
	if strings.Contains(pom, "<groupId>org.junit.jupiter</groupId>") ||
		strings.Contains(pom, "<artifactId>junit-jupiter</artifactId>") {
		return pom
	}
	if idx := findTopLevelDependenciesClose(pom); idx >= 0 {
		return pom[:idx] + javaRepoLevelJUnitJupiterDependency + pom[idx:]
	}
	deps := "  <dependencies>\n" + javaRepoLevelJUnitJupiterDependency + "  </dependencies>\n\n"
	if idx := strings.Index(pom, "<build>"); idx >= 0 {
		return pom[:idx] + deps + pom[idx:]
	}
	if idx := strings.LastIndex(pom, "</project>"); idx >= 0 {
		return pom[:idx] + deps + pom[idx:]
	}
	return pom + "\n" + deps
}

func ensureJavaRepoLevelBuildPlugin(pom, artifactID, pluginXML string) string {
	if strings.Contains(pom, "<artifactId>"+artifactID+"</artifactId>") {
		if artifactID == "pitest-maven" && !strings.Contains(pom, "<artifactId>pitest-junit5-plugin</artifactId>") {
			return ensurePitestJUnit5PluginDependency(pom)
		}
		return pom
	}
	if idx := findTopLevelBuildPluginsClose(pom); idx >= 0 {
		return pom[:idx] + pluginXML + pom[idx:]
	}
	plugins := "    <plugins>\n" + pluginXML + "    </plugins>\n"
	if idx := findTopLevelBuildClose(pom); idx >= 0 {
		return pom[:idx] + plugins + pom[idx:]
	}
	build := "  <build>\n" + plugins + "  </build>\n\n"
	if idx := strings.LastIndex(pom, "</project>"); idx >= 0 {
		return pom[:idx] + build + pom[idx:]
	}
	return pom + "\n" + build
}

func ensurePitestJUnit5PluginDependency(pom string) string {
	const pluginStart = "<plugin>"
	const pluginEnd = "</plugin>"
	artifactRe := regexp.MustCompile(`(?s)<artifactId>\s*pitest-maven\s*</artifactId>`)
	var b strings.Builder
	cursor := 0
	for {
		startRel := strings.Index(pom[cursor:], pluginStart)
		if startRel < 0 {
			b.WriteString(pom[cursor:])
			break
		}
		start := cursor + startRel
		endRel := strings.Index(pom[start:], pluginEnd)
		if endRel < 0 {
			b.WriteString(pom[cursor:])
			break
		}
		end := start + endRel + len(pluginEnd)
		b.WriteString(pom[cursor:start])
		block := pom[start:end]
		if artifactRe.MatchString(block) && !strings.Contains(block, "<artifactId>pitest-junit5-plugin</artifactId>") {
			block = insertPitestJUnit5Dependency(block)
		}
		b.WriteString(block)
		cursor = end
	}
	return b.String()
}

func insertPitestJUnit5Dependency(pluginBlock string) string {
	dep := `          <dependency>
            <groupId>org.pitest</groupId>
            <artifactId>pitest-junit5-plugin</artifactId>
            <version>1.2.1</version>
          </dependency>
`
	if idx := strings.Index(pluginBlock, "</dependencies>"); idx >= 0 {
		return pluginBlock[:idx] + dep + pluginBlock[idx:]
	}
	deps := "        <dependencies>\n" + dep + "        </dependencies>\n"
	if idx := strings.Index(pluginBlock, "</plugin>"); idx >= 0 {
		return pluginBlock[:idx] + deps + pluginBlock[idx:]
	}
	return pluginBlock
}

func findTopLevelDependenciesClose(pom string) int {
	cursor := 0
	for {
		openRel := strings.Index(pom[cursor:], "<dependencies>")
		if openRel < 0 {
			return -1
		}
		open := cursor + openRel
		closeRel := strings.Index(pom[open:], "</dependencies>")
		if closeRel < 0 {
			return -1
		}
		close := open + closeRel
		before := pom[:open]
		depMgmtOpen := strings.LastIndex(before, "<dependencyManagement>")
		depMgmtClose := strings.LastIndex(before, "</dependencyManagement>")
		if depMgmtOpen < 0 || depMgmtOpen < depMgmtClose {
			return close
		}
		cursor = close + len("</dependencies>")
	}
}

func findTopLevelBuildClose(pom string) int {
	open := strings.Index(pom, "<build>")
	if open < 0 {
		return -1
	}
	closeRel := strings.Index(pom[open:], "</build>")
	if closeRel < 0 {
		return -1
	}
	return open + closeRel
}

func findTopLevelBuildPluginsClose(pom string) int {
	buildOpen := strings.Index(pom, "<build>")
	if buildOpen < 0 {
		return -1
	}
	buildCloseRel := strings.Index(pom[buildOpen:], "</build>")
	if buildCloseRel < 0 {
		return -1
	}
	buildClose := buildOpen + buildCloseRel
	cursor := buildOpen
	for {
		openRel := strings.Index(pom[cursor:buildClose], "<plugins>")
		if openRel < 0 {
			return -1
		}
		open := cursor + openRel
		closeRel := strings.Index(pom[open:buildClose], "</plugins>")
		if closeRel < 0 {
			return -1
		}
		close := open + closeRel
		before := pom[buildOpen:open]
		pluginMgmtOpen := strings.LastIndex(before, "<pluginManagement>")
		pluginMgmtClose := strings.LastIndex(before, "</pluginManagement>")
		if pluginMgmtOpen < 0 || pluginMgmtOpen < pluginMgmtClose {
			return close
		}
		cursor = close + len("</plugins>")
	}
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
	if loc := javaRepoMainSourceRootRe.FindStringIndex(targetFile); loc != nil {
		prefix := strings.TrimSuffix(targetFile[:loc[0]], "/")
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
	out := []string{
		"-q",
		"-B",
		"--no-transfer-progress",
		"-Dmaven.repo.local=" + javaMavenLocalRepo(),
		"-Dmaven.artifact.threads=1",
		"-Denforcer.skip=true",
		"-Dspotless.check.skip=true",
		"-Dspotless.apply.skip=true",
		"-Dspotless.skip=true",
		"-Dpalantir.format.skip=true",
		"-Drat.skip=true",
		"-Dcheckstyle.skip=true",
		"-Dpmd.skip=true",
		"-Dcpd.skip=true",
		"-Dspotbugs.skip=true",
		"-Dforbiddenapis.skip=true",
		"-Danimal.sniffer.skip=true",
		"-Djapicmp.skip=true",
		"-Drevapi.skip=true",
		"-Dmodernizer.skip=true",
		"-Dgpg.skip=true",
		"-Dsign.skip=true",
		"-Dmaven.javadoc.skip=true",
		"-DskipJavadocs=true",
		"-Dsource.skip=true",
	}
	moduleDir = strings.TrimSpace(filepath.ToSlash(moduleDir))
	if moduleDir != "" && moduleDir != "." {
		out = append(out, "-pl", moduleDir, "-am")
	}
	out = append(out, args...)
	return out
}

func javaMavenLocalRepo() string {
	if repo := strings.TrimSpace(os.Getenv("UTBENCH_MAVEN_REPO_LOCAL")); repo != "" {
		return repo
	}
	return filepath.Join(os.TempDir(), "utbench-m2-repository")
}

func runJavaMavenCommand(ctx context.Context, workdir, moduleDir string, args ...string) ([]byte, error) {
	repo := javaMavenLocalRepo()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return nil, err
	}
	javaMavenMu.Lock()
	defer javaMavenMu.Unlock()
	return runCommandWithProcessGroupKill(ctx, "mvn", javaMavenArgsForModule(moduleDir, args...), workdir, nil)
}

func javaCompileCheckRepoLevel(workdir, moduleDir string) (bool, string) {
	compileTimeout := defaultTestTimeoutSeconds * 3
	runCtx, cancel := context.WithTimeout(context.Background(), time.Duration(compileTimeout)*time.Second)
	defer cancel()
	output, err := runJavaMavenCommand(runCtx, workdir, moduleDir, "-DskipTests", "test-compile")
	if runCtx.Err() != nil {
		return false, fmt.Sprintf("java compile timed out after %ds", compileTimeout)
	}
	if err != nil {
		return false, javaCommandFailureMessage(output, err, 3000)
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
	output, err := runJavaMavenCommand(runCtx, workdir, moduleDir, "-Dtest="+testClassName, "-DfailIfNoTests=false", "-Dsurefire.failIfNoSpecifiedTests=false", "test")
	latency := int(time.Since(started).Milliseconds())
	if runCtx.Err() != nil {
		return false, fmt.Sprintf("java test timed out after %ds", timeoutSeconds), latency
	}
	if err == nil {
		return true, string(output), latency
	}
	return false, javaCommandFailureMessage(output, err, 4000), latency
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
	args := []string{
		"-Dtest=" + testClassName,
		"-DfailIfNoTests=false",
		"-Dsurefire.failIfNoSpecifiedTests=false",
		"org.jacoco:jacoco-maven-plugin:prepare-agent",
		"test",
		"org.jacoco:jacoco-maven-plugin:report",
	}
	if out, err := runJavaMavenCommand(runCtx, workdir, moduleDir, args...); err != nil {
		if runCtx.Err() != nil {
			return 0, 0, fmt.Sprintf("java coverage timed out after %ds", timeoutSeconds)
		}
		return 0, 0, "jacoco run failed: " + javaCommandFailureMessage(out, err, 1200)
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

func collectJavaMutationRepoLevel(ctx context.Context, workdir, moduleDir, className, packageName, testClassName string, timeoutSeconds int, testPassRate *float64, testPassed, testTotal int) (float64, mutationStats, string) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = MutationTimeoutSeconds
	}
	targetClass := qualifiedJavaClassName(packageName, className)
	if targetClass == "" {
		return 0, mutationStats{}, "pitest: missing target class"
	}
	targetTests := testClassName
	if targetTests == "" {
		targetTests = className + "Test"
	}
	targetTests = qualifiedJavaClassName(packageName, targetTests)

	minPassRate := GetMinPassRateForTool("pitest")
	passed := 0
	total := 0
	if testPassed > 0 || testTotal > 0 {
		passed = testPassed
		total = testTotal
	} else if testPassRate != nil {
		total = 100
		passed = int(math.Round(*testPassRate * float64(total)))
		if passed > total {
			passed = total
		}
	} else {
		passed = 1
		total = 1
	}
	checkResult := CheckTestPassRate(passed, total, "PITest", minPassRate)
	if !checkResult.ShouldRun {
		return 0, mutationStats{}, checkResult.Message
	}

	runCtx, cancelRun := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancelRun()
	if strings.TrimSpace(moduleDir) != "" && moduleDir != "." {
		installArgs := []string{
			"-DskipTests",
			"-DskipITs",
			"install",
		}
		installOut, installErr := runJavaMavenCommand(runCtx, workdir, moduleDir, installArgs...)
		if runCtx.Err() != nil {
			return 0, mutationStats{}, fmt.Sprintf("pitest dependency install timed out after %ds", timeoutSeconds)
		}
		if installErr != nil {
			return 0, mutationStats{}, formatMutationToolError("pitest", "pitest dependency install failed", installErr, installOut, nil, nil)
		}
	}
	reportDir := workdir
	if strings.TrimSpace(moduleDir) != "" && moduleDir != "." {
		reportDir = filepath.Join(workdir, filepath.FromSlash(moduleDir))
	}
	attempts := javaPitestTargetClassAttempts(packageName, targetClass)
	var lastOut []byte
	var lastErr error
	var stats mutationStats
	var parseErr string
	for i, targetPattern := range attempts {
		_ = os.RemoveAll(filepath.Join(reportDir, "target", "pit-reports"))
		args := javaPitestArgs(targetPattern, targetTests)
		lastOut, lastErr = runJavaMavenCommand(runCtx, reportDir, "", args...)
		if runCtx.Err() != nil {
			return 0, mutationStats{}, fmt.Sprintf("pitest timed out after %ds", timeoutSeconds)
		}
		stats, parseErr = parsePitXML(reportDir)
		noMutants := pitOutputReportsNoMutants(string(lastOut)) || (parseErr == "" && stats.Total <= 0)
		if noMutants && i+1 < len(attempts) {
			continue
		}
		if noMutants {
			return 0, mutationStats{}, ""
		}
		break
	}
	if parseErr != "" {
		return 0, stats, formatMutationToolError("pitest", "pitest parse error", lastErr, lastOut, nil, nil)
	}
	if stats.Total <= 0 {
		return 0, stats, ""
	}
	processed := stats.Killed + stats.Survived + stats.NoTests + stats.Timeout + stats.Skipped + stats.Suspicious
	if processed <= 0 {
		return 0, stats, formatMutationToolError("pitest", "pitest did not execute any mutants", lastErr, lastOut, nil, nil)
	}
	if stats.Killed+stats.Survived <= 0 {
		if stats.NoTests+stats.Timeout+stats.Skipped+stats.Suspicious > 0 {
			return 0, stats, ""
		}
		return 0, stats, formatMutationToolError("pitest", "pitest no killed/survived results", lastErr, lastOut, nil, nil)
	}
	score := round(float64(stats.Killed)/float64(stats.Killed+stats.Survived), 6)
	return score, stats, ""
}

func javaPitestArgs(targetClasses, targetTests string) []string {
	return []string{
		"-DtargetClasses=" + targetClasses,
		"-DtargetTests=" + targetTests,
		"-DoutputFormats=XML,CSV",
		"-DtimestampedReports=false",
		"-DfailWhenNoMutations=false",
		"-DskipFailingTests=true",
		"-Dthreads=1",
		"-DmutationThreshold=0",
		"-DcoverageThreshold=0",
		"-DtimeoutConstant=5000",
		"-DwithHistory=false",
		"-Dmutators=STRONGER",
		"org.pitest:pitest-maven:mutationCoverage",
	}
}

func javaPitestTargetClassAttempts(packageName, targetClass string) []string {
	targetClass = strings.TrimSpace(targetClass)
	if targetClass == "" {
		return nil
	}
	out := []string{targetClass + "*"}
	packageName = strings.TrimSpace(packageName)
	if packageName != "" {
		pkgTarget := packageName + ".*"
		if pkgTarget != out[0] {
			out = append(out, pkgTarget)
		}
	}
	return out
}

func pitOutputReportsNoMutants(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "no mutations found") ||
		strings.Contains(lower, "created 0 mutation test units") ||
		strings.Contains(lower, "skipping coverage and analysis as no mutations found")
}

func qualifiedJavaClassName(packageName, className string) string {
	packageName = strings.TrimSpace(packageName)
	className = strings.TrimSpace(className)
	if className == "" {
		return ""
	}
	if packageName == "" || strings.Contains(className, ".") {
		return className
	}
	return packageName + "." + className
}

func javaCommandFailureMessage(output []byte, err error, max int) string {
	msg := trimErr(string(output), max)
	if msg != "" {
		return msg
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
