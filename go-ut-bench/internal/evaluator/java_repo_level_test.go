package evaluator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ut-bench/internal/contracts"
)

func TestJavaRepoLevelPrepareWorkspaceUsesProjectRootAndModuleDir(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "datasets", "java", "java_code_files_repo_level", "oss", "gson")
	moduleRoot := filepath.Join(repo, "gson")
	sourceDir := filepath.Join(moduleRoot, "src", "main", "java", "com", "google", "gson")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helperDir := filepath.Join(moduleRoot, "src", "test", "java", "com", "google", "gson", "common")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existingTestDir := filepath.Join(moduleRoot, "src", "test", "java", "com", "google", "gson")
	if err := os.MkdirAll(existingTestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rootPom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <build>
    <plugins>
      <plugin>
        <groupId>org.sonatype.central</groupId>
        <artifactId>central-publishing-maven-plugin</artifactId>
        <version>0.10.0</version>
        <extensions>true</extensions>
      </plugin>
    </plugins>
  </build>
</project>`
	if err := os.WriteFile(filepath.Join(repo, "pom.xml"), []byte(rootPom), 0o644); err != nil {
		t.Fatal(err)
	}
	modulePom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <dependencies>
    <dependency>
      <groupId>junit</groupId>
      <artifactId>junit</artifactId>
      <scope>test</scope>
    </dependency>
  </dependencies>
</project>`
	if err := os.WriteFile(filepath.Join(moduleRoot, "pom.xml"), []byte(modulePom), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "JsonParser.java")
	if err := os.WriteFile(sourcePath, []byte("package com.google.gson;\n\nclass JsonParser {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helperDir, "MoreAsserts.java"), []byte("package com.google.gson.common;\n\npublic final class MoreAsserts {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingTestDir, "ExistingBehaviorTest.java"), []byte("package com.google.gson;\n\npublic final class ExistingBehaviorTest {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generatedTest := filepath.Join(root, "JsonParserTest.java")
	if err := os.WriteFile(generatedTest, []byte("import org.junit.jupiter.api.Test;\nclass JsonParserTest { @Test void parses() {} }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := (&JavaEvaluator{}).PrepareWorkspace(contracts.GeneratedCase{
		Language:          "java",
		SampleID:          "json_parser",
		SamplePath:        sourcePath,
		GeneratedTestPath: generatedTest,
		Success:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(ws.Workdir)

	if ws.Extra["isRepoLevel"] != "true" {
		t.Fatalf("expected repo_level workspace, got %+v", ws.Extra)
	}
	if ws.Extra["moduleDir"] != "gson" {
		t.Fatalf("moduleDir = %q", ws.Extra["moduleDir"])
	}
	wantTest := "gson/src/test/java/com/google/gson/JsonParserTest.java"
	if ws.TestPath != wantTest {
		t.Fatalf("test path = %q, want %q", ws.TestPath, wantTest)
	}
	raw, err := os.ReadFile(filepath.Join(ws.Workdir, filepath.FromSlash(wantTest)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "package com.google.gson;") {
		t.Fatalf("generated test package was not normalized:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(ws.Workdir, "pom.xml")); err != nil {
		t.Fatalf("root pom not copied: %v", err)
	}
	rootPomRaw, err := os.ReadFile(filepath.Join(ws.Workdir, "pom.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rootPomRaw), "<extensions>true</extensions>") {
		t.Fatalf("central publishing extension was not disabled:\n%s", rootPomRaw)
	}
	modulePomRaw, err := os.ReadFile(filepath.Join(ws.Workdir, "gson", "pom.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(modulePomRaw), "<artifactId>junit-jupiter</artifactId>") {
		t.Fatalf("module pom missing JUnit Jupiter dependency:\n%s", modulePomRaw)
	}
	if !strings.Contains(string(modulePomRaw), "<artifactId>maven-surefire-plugin</artifactId>") {
		t.Fatalf("module pom missing Surefire plugin:\n%s", modulePomRaw)
	}
	if !strings.Contains(string(modulePomRaw), "<artifactId>pitest-maven</artifactId>") ||
		!strings.Contains(string(modulePomRaw), "<artifactId>pitest-junit5-plugin</artifactId>") {
		t.Fatalf("module pom missing PITest JUnit 5 plugin:\n%s", modulePomRaw)
	}
	if _, err := os.Stat(filepath.Join(ws.Workdir, "gson", "src", "test", "java", "com", "google", "gson", "common", "MoreAsserts.java")); !os.IsNotExist(err) {
		t.Fatalf("existing test helper source should be skipped, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Workdir, "gson", "src", "test", "java", "com", "google", "gson", "ExistingBehaviorTest.java")); !os.IsNotExist(err) {
		t.Fatalf("existing test case should be skipped, stat err=%v", err)
	}
}

func TestJavaRepoLevelPOMRewrite(t *testing.T) {
	pom := `<project>
  <build><plugins>
    <plugin>
      <groupId>org.sonatype.central</groupId>
      <artifactId>central-publishing-maven-plugin</artifactId>
      <extensions>true</extensions>
    </plugin>
  </plugins></build>
</project>`
	rewritten := disableCentralPublishingExtension(pom)
	if strings.Contains(rewritten, "<extensions>true</extensions>") {
		t.Fatalf("extension still enabled:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "<extensions>false</extensions>") {
		t.Fatalf("extension was not rewritten:\n%s", rewritten)
	}
	relaxed := relaxJavaRepoLevelStrictWarnings(`<project>
  <build>
    <plugins>
      <plugin>
        <configuration>
          <failOnWarning>true</failOnWarning>
          <failOnWarnings>true</failOnWarnings>
        </configuration>
      </plugin>
    </plugins>
  </build>
</project>`)
	if strings.Contains(relaxed, "<failOnWarning>true</failOnWarning>") || strings.Contains(relaxed, "<failOnWarnings>true</failOnWarnings>") {
		t.Fatalf("strict warning settings were not relaxed:\n%s", relaxed)
	}
	disabled := disableJavaRepoLevelPlugin(`<project>
  <build><plugins>
    <plugin>
      <artifactId>proguard-maven-plugin</artifactId>
      <configuration>
        <skip>${maven.test.skip}</skip>
        <obfuscate>true</obfuscate>
      </configuration>
    </plugin>
    <plugin>
      <artifactId>another-plugin</artifactId>
      <configuration><skip>false</skip></configuration>
    </plugin>
  </plugins></build>
</project>`, "proguard-maven-plugin")
	if strings.Contains(disabled, "<artifactId>proguard-maven-plugin</artifactId>") {
		t.Fatalf("proguard plugin was not removed:\n%s", disabled)
	}
	if !strings.Contains(disabled, "<artifactId>another-plugin</artifactId>") || !strings.Contains(disabled, "<skip>false</skip>") {
		t.Fatalf("unrelated plugin was changed:\n%s", disabled)
	}
	disabledGate := disableJavaRepoLevelPlugin(`<project>
  <build><plugins>
    <plugin>
      <artifactId>spotless-maven-plugin</artifactId>
      <configuration><encoding>UTF-8</encoding></configuration>
    </plugin>
  </plugins></build>
</project>`, "spotless-maven-plugin")
	if strings.Contains(disabledGate, "<artifactId>spotless-maven-plugin</artifactId>") {
		t.Fatalf("quality gate plugin was not removed:\n%s", disabledGate)
	}

	withJUnit := ensureJUnitJupiterDependency(`<project>
  <modelVersion>4.0.0</modelVersion>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>example</groupId>
        <artifactId>managed</artifactId>
      </dependency>
    </dependencies>
  </dependencyManagement>
  <dependencies>
  </dependencies>
</project>`)
	if !strings.Contains(withJUnit, "<artifactId>junit-jupiter</artifactId>") {
		t.Fatalf("JUnit dependency not inserted:\n%s", withJUnit)
	}
	if strings.Index(withJUnit, "<artifactId>junit-jupiter</artifactId>") < strings.Index(withJUnit, "</dependencyManagement>") {
		t.Fatalf("JUnit dependency was inserted into dependencyManagement:\n%s", withJUnit)
	}
	again := ensureJUnitJupiterDependency(withJUnit)
	if strings.Count(again, "<artifactId>junit-jupiter</artifactId>") != 1 {
		t.Fatalf("JUnit dependency duplicated:\n%s", again)
	}

	withArgLine := ensureMavenProperty(`<project>
  <modelVersion>4.0.0</modelVersion>
  <build><plugins>
    <plugin>
      <artifactId>maven-surefire-plugin</artifactId>
      <configuration>
        <argLine>-Xss640k</argLine>
      </configuration>
    </plugin>
  </plugins></build>
</project>`, "argLine", "")
	withArgLine = ensureSurefireArgLinePreservesJacoco(withArgLine)
	if !strings.Contains(withArgLine, "<argLine></argLine>") {
		t.Fatalf("default argLine property was not inserted:\n%s", withArgLine)
	}
	if !strings.Contains(withArgLine, "<argLine>${argLine} -Xss640k</argLine>") {
		t.Fatalf("Surefire argLine does not preserve JaCoCo argLine:\n%s", withArgLine)
	}
	if strings.Count(ensureSurefireArgLinePreservesJacoco(withArgLine), "${argLine}") != strings.Count(withArgLine, "${argLine}") {
		t.Fatalf("Surefire argLine rewrite is not idempotent:\n%s", withArgLine)
	}
}

func TestJavaPitestTargetClassAttemptsFallbackToPackage(t *testing.T) {
	got := javaPitestTargetClassAttempts("com.example.pkg", "com.example.pkg.Target")
	want := []string{"com.example.pkg.Target*", "com.example.pkg.*"}
	if len(got) != len(want) {
		t.Fatalf("attempt count = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestJavaPitestArgsUsesStrongerMutatorsAndNoHistory(t *testing.T) {
	args := strings.Join(javaPitestArgs("com.example.Target*", "com.example.TargetTest"), " ")
	for _, want := range []string{
		"-DtargetClasses=com.example.Target*",
		"-DtargetTests=com.example.TargetTest",
		"-DwithHistory=false",
		"-Dmutators=STRONGER",
		"org.pitest:pitest-maven:mutationCoverage",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("pitest args missing %q: %s", want, args)
		}
	}
}

func TestJavaRepoLevelTestPathForVersionedSourceRoot(t *testing.T) {
	tests := []struct {
		name       string
		targetFile string
		want       string
	}{
		{
			name:       "root java11 source set",
			targetFile: "src/main/java11/org/jsoup/helper/HttpClientExecutor.java",
			want:       "src/test/java/org/jsoup/helper/HttpClientExecutorTest.java",
		},
		{
			name:       "module java source set",
			targetFile: "gson/src/main/java/com/google/gson/JsonParser.java",
			want:       "gson/src/test/java/org/jsoup/helper/HttpClientExecutorTest.java",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := javaTestRelForTarget(tt.targetFile, "org.jsoup.helper", "HttpClientExecutorTest")
			if got != tt.want {
				t.Fatalf("test path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJavaRepoLevelSkipsVersionedTestJavaSourceRoots(t *testing.T) {
	tests := []struct {
		rel  string
		skip bool
	}{
		{rel: "src/test/java/org/jsoup/helper/HttpClientExecutorTest.java", skip: true},
		{rel: "src/test/java11/org/jsoup/helper/HttpClientExecutorTest.java", skip: true},
		{rel: "module/src/test/java17/com/example/Fixture.java", skip: true},
		{rel: "src/test/resources/example.json", skip: false},
		{rel: "src/main/java/org/jsoup/helper/HttpClientExecutor.java", skip: false},
	}
	for _, tt := range tests {
		t.Run(tt.rel, func(t *testing.T) {
			if got := isJavaRepoExistingTestCase(tt.rel); got != tt.skip {
				t.Fatalf("skip = %v, want %v", got, tt.skip)
			}
		})
	}
}

func TestJavaAssertionDensityReadsRepoLevelRelativeTestPath(t *testing.T) {
	workdir := t.TempDir()
	testRel := "src/test/java11/org/jsoup/helper/HttpClientExecutorTest.java"
	testPath := filepath.Join(workdir, filepath.FromSlash(testRel))
	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `package org.jsoup.helper;

import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

class HttpClientExecutorTest {
  @Test
  void executes() {
    assertEquals("ok", "ok");
    assertTrue(true);
  }
}
`
	if err := os.WriteFile(testPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	assertions, tests, density := (&JavaEvaluator{}).EstimateAssertionDensity(workdir, testRel)
	if assertions != 2 {
		t.Fatalf("assertions = %d, want 2", assertions)
	}
	if tests != 1 {
		t.Fatalf("tests = %d, want 1", tests)
	}
	if density != 2 {
		t.Fatalf("density = %v, want 2", density)
	}
}

func TestJavaMavenArgsSkipRepoQualityGates(t *testing.T) {
	args := javaMavenArgsForModule("module-a", "test-compile")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-Denforcer.skip=true",
		"-Dspotless.check.skip=true",
		"-Dspotless.apply.skip=true",
		"-Dspotless.skip=true",
		"-Dpalantir.format.skip=true",
		"-Drat.skip=true",
		"-Dcheckstyle.skip=true",
		"-Dpmd.skip=true",
		"-Dspotbugs.skip=true",
		"-Dgpg.skip=true",
		"-Dmaven.javadoc.skip=true",
		"-Dsource.skip=true",
		"-pl module-a -am",
		"test-compile",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("maven args missing %q:\n%s", want, joined)
		}
	}
}

func TestJavaMavenArgsUseContainerRepoForDockerBackend(t *testing.T) {
	old := GetEvalBackend()
	SetEvalBackend(NewDockerBackend("utbench:test"))
	defer SetEvalBackend(old)

	workdir := t.TempDir()
	args := javaMavenArgsForModule("module-a", "test-compile")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-Dmaven.repo.local=/utbench-cache/m2") {
		t.Fatalf("docker maven args should use container repo path:\n%s", joined)
	}
	hostRepo := javaMavenHostLocalRepo(workdir)
	if strings.TrimSpace(hostRepo) == "" || !strings.Contains(filepath.ToSlash(hostRepo), ".m2-cache/repository") {
		t.Fatalf("unexpected host maven repo path: %s", hostRepo)
	}
}

func TestRelaxJavaRepoLevelStrictWarningsRemovesErrorProneArg(t *testing.T) {
	pom := `<project>
  <build>
    <plugins>
      <plugin>
        <configuration>
          <failOnWarning>true</failOnWarning>
          <compilerArgs>
            <arg>-Xplugin:ErrorProne
              -Xep:NotJavadoc:OFF
            </arg>
            <arg>-parameters</arg>
          </compilerArgs>
        </configuration>
      </plugin>
    </plugins>
  </build>
</project>`
	updated := relaxJavaRepoLevelStrictWarnings(pom)
	if strings.Contains(updated, "ErrorProne") || strings.Contains(updated, "-Xplugin") {
		t.Fatalf("ErrorProne compiler arg was not removed:\n%s", updated)
	}
	if !strings.Contains(updated, "<failOnWarning>false</failOnWarning>") {
		t.Fatalf("failOnWarning was not relaxed:\n%s", updated)
	}
	if !strings.Contains(updated, "<arg>-parameters</arg>") {
		t.Fatalf("unrelated compiler arg should be preserved:\n%s", updated)
	}
}

func TestPitOutputReportsNoMutants(t *testing.T) {
	output := "PIT >> INFO : Created 0 mutation test units in pre scan\nPIT >> WARNING : No mutations found"
	if !pitOutputReportsNoMutants(output) {
		t.Fatalf("expected no-mutants output to be detected")
	}
}
