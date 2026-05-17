package agentconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-ut-bench/internal/contracts"

	"gopkg.in/yaml.v3"
)

const (
	KindModelAPI = "model_api"
	KindCLIAgent = "cli_agent"
	NoSkill      = "no_skill"
)

type FrameworkSpec struct {
	Name                     string              `json:"name"`
	Kind                     string              `json:"kind"`
	Enabled                  bool                `json:"enabled"`
	Command                  string              `json:"command,omitempty"`
	Sandbox                  SandboxSpec         `json:"sandbox,omitempty"`
	DockerImage              string              `json:"docker_image,omitempty"`
	DockerImages             map[string]string   `json:"docker_images,omitempty"`
	SandboxMode              string              `json:"sandbox_mode,omitempty"`
	TimeoutSeconds           int                 `json:"timeout_seconds,omitempty"`
	OutputGlobs              []string            `json:"output_globs,omitempty"`
	Env                      map[string]string   `json:"env,omitempty"`
	EnvFromHost              []string            `json:"env_from_host,omitempty"`
	Preflight                map[string][]string `json:"preflight,omitempty"`
	ForbiddenCommandPatterns []string            `json:"forbidden_command_patterns,omitempty"`
	CompatibleModels         []string            `json:"compatible_models,omitempty"`
	CompatibleLangs          []string            `json:"compatible_languages,omitempty"`
	DisableNoSkill           bool                `json:"disable_no_skill,omitempty"`
	NetworkDisabled          bool                `json:"network_disabled,omitempty"`
	CPU                      string              `json:"cpu,omitempty"`
	Memory                   string              `json:"memory,omitempty"`
}

type SandboxSpec struct {
	Provider        string            `json:"provider,omitempty"`
	Mode            string            `json:"mode,omitempty"`
	Image           string            `json:"image,omitempty"`
	Images          map[string]string `json:"images,omitempty"`
	TimeoutSeconds  int               `json:"timeout_seconds,omitempty"`
	NetworkDisabled bool              `json:"network_disabled,omitempty"`
	CPU             string            `json:"cpu,omitempty"`
	Memory          string            `json:"memory,omitempty"`
}

type SubjectEntry struct {
	ID           string   `yaml:"id"`
	Enabled      *bool    `yaml:"enabled"`
	Kind         string   `yaml:"kind"`
	Framework    string   `yaml:"framework"`
	Model        string   `yaml:"model"`
	Skill        string   `yaml:"skill"`
	SkillVersion string   `yaml:"skill_version"`
	Version      string   `yaml:"version"`
	Labels       []string `yaml:"labels"`
	Tags         []string `yaml:"tags"`
}

type ResolvedSubject struct {
	Spec      contracts.SubjectSpec
	Framework FrameworkSpec
	Skill     contracts.SkillSpec
}

type fileConfig struct {
	Models     []string `yaml:"models"`
	Frameworks map[string]struct {
		Enabled *bool  `yaml:"enabled"`
		Kind    string `yaml:"kind"`
		Command string `yaml:"command"`
		Sandbox struct {
			Provider        string            `yaml:"provider"`
			Mode            string            `yaml:"mode"`
			Image           string            `yaml:"image"`
			Images          map[string]string `yaml:"images"`
			TimeoutSeconds  int               `yaml:"timeout_seconds"`
			NetworkDisabled *bool             `yaml:"network_disabled"`
			CPU             string            `yaml:"cpu"`
			Memory          string            `yaml:"memory"`
		} `yaml:"sandbox"`
		DockerImage              string              `yaml:"docker_image"`
		DockerImages             map[string]string   `yaml:"docker_images"`
		SandboxMode              string              `yaml:"sandbox_mode"`
		TimeoutSeconds           int                 `yaml:"timeout_seconds"`
		OutputGlobs              []string            `yaml:"output_globs"`
		Env                      map[string]string   `yaml:"env"`
		EnvFromHost              []string            `yaml:"env_from_host"`
		Preflight                map[string][]string `yaml:"preflight"`
		ForbiddenCommandPatterns []string            `yaml:"forbidden_command_patterns"`
		CompatibleModels         []string            `yaml:"compatible_models"`
		CompatibleLanguages      []string            `yaml:"compatible_languages"`
		DisableNoSkill           bool                `yaml:"disable_no_skill"`
		NetworkDisabled          *bool               `yaml:"network_disabled"`
		CPU                      string              `yaml:"cpu"`
		Memory                   string              `yaml:"memory"`
	} `yaml:"frameworks"`
	Skills   map[string]skillConfig `yaml:"skills"`
	Subjects []SubjectEntry         `yaml:"subjects"`
}

type skillConfig struct {
	Enabled              *bool                         `yaml:"enabled"`
	Version              string                        `yaml:"version"`
	DefaultVersion       string                        `yaml:"default_version"`
	Description          string                        `yaml:"description"`
	InstructionPath      string                        `yaml:"instruction_path"`
	Files                []string                      `yaml:"files"`
	InjectMode           string                        `yaml:"inject_mode"`
	CompatibleFrameworks []string                      `yaml:"compatible_frameworks"`
	CompatibleLanguages  []string                      `yaml:"compatible_languages"`
	Versions             map[string]skillVersionConfig `yaml:"versions"`
}

type skillVersionConfig struct {
	Enabled              *bool    `yaml:"enabled"`
	Description          string   `yaml:"description"`
	InstructionPath      string   `yaml:"instruction_path"`
	Files                []string `yaml:"files"`
	InjectMode           string   `yaml:"inject_mode"`
	CompatibleFrameworks []string `yaml:"compatible_frameworks"`
	CompatibleLanguages  []string `yaml:"compatible_languages"`
}

func Load(path string, modelNames []string, selected []string) ([]ResolvedSubject, error) {
	modelNames = uniqueNonEmpty(modelNames)
	if len(modelNames) == 0 {
		return nil, fmt.Errorf("at least one model is required to build subjects")
	}
	if strings.TrimSpace(path) == "" {
		return filterSubjects(defaultModelAPISubjects(modelNames), selected)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg fileConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if len(cfg.Models) > 0 {
		modelNames = intersect(modelNames, uniqueNonEmpty(cfg.Models))
	}
	if len(modelNames) == 0 {
		return nil, fmt.Errorf("agents config selected no models")
	}
	resolved := defaultModelAPISubjects(modelNames)
	frameworks := normalizeFrameworks(cfg)
	skills := normalizeSkills(cfg, filepath.Dir(path))
	if len(cfg.Subjects) > 0 {
		for _, entry := range cfg.Subjects {
			if entry.Enabled != nil && !*entry.Enabled {
				continue
			}
			rs, ok, err := resolveSubjectEntry(entry, frameworks, skills)
			if err != nil {
				return nil, err
			}
			if ok {
				resolved = append(resolved, rs)
			}
		}
	} else {
		resolved = append(resolved, expandCartesian(modelNames, frameworks, skills)...)
	}
	resolved = dedupeSubjects(resolved)
	return filterSubjects(resolved, selected)
}

func normalizeFrameworks(cfg fileConfig) map[string]FrameworkSpec {
	out := map[string]FrameworkSpec{}
	for name, item := range cfg.Frameworks {
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		if !enabled {
			continue
		}
		kind := strings.TrimSpace(item.Kind)
		if kind == "" {
			kind = KindCLIAgent
		}
		sandbox := normalizeSandboxSpec(item)
		out[name] = FrameworkSpec{
			Name:                     name,
			Kind:                     kind,
			Enabled:                  true,
			Command:                  item.Command,
			Sandbox:                  sandbox,
			DockerImage:              sandbox.Image,
			DockerImages:             normalizeStringMap(sandbox.Images),
			SandboxMode:              sandbox.Mode,
			TimeoutSeconds:           sandbox.TimeoutSeconds,
			OutputGlobs:              item.OutputGlobs,
			Env:                      item.Env,
			EnvFromHost:              uniqueNonEmpty(item.EnvFromHost),
			Preflight:                normalizeStringSliceMap(item.Preflight),
			ForbiddenCommandPatterns: uniqueNonEmpty(item.ForbiddenCommandPatterns),
			CompatibleModels:         item.CompatibleModels,
			CompatibleLangs:          item.CompatibleLanguages,
			DisableNoSkill:           item.DisableNoSkill,
			NetworkDisabled:          sandbox.NetworkDisabled,
			CPU:                      sandbox.CPU,
			Memory:                   sandbox.Memory,
		}
	}
	return out
}

func normalizeSandboxSpec(item struct {
	Enabled *bool  `yaml:"enabled"`
	Kind    string `yaml:"kind"`
	Command string `yaml:"command"`
	Sandbox struct {
		Provider        string            `yaml:"provider"`
		Mode            string            `yaml:"mode"`
		Image           string            `yaml:"image"`
		Images          map[string]string `yaml:"images"`
		TimeoutSeconds  int               `yaml:"timeout_seconds"`
		NetworkDisabled *bool             `yaml:"network_disabled"`
		CPU             string            `yaml:"cpu"`
		Memory          string            `yaml:"memory"`
	} `yaml:"sandbox"`
	DockerImage              string              `yaml:"docker_image"`
	DockerImages             map[string]string   `yaml:"docker_images"`
	SandboxMode              string              `yaml:"sandbox_mode"`
	TimeoutSeconds           int                 `yaml:"timeout_seconds"`
	OutputGlobs              []string            `yaml:"output_globs"`
	Env                      map[string]string   `yaml:"env"`
	EnvFromHost              []string            `yaml:"env_from_host"`
	Preflight                map[string][]string `yaml:"preflight"`
	ForbiddenCommandPatterns []string            `yaml:"forbidden_command_patterns"`
	CompatibleModels         []string            `yaml:"compatible_models"`
	CompatibleLanguages      []string            `yaml:"compatible_languages"`
	DisableNoSkill           bool                `yaml:"disable_no_skill"`
	NetworkDisabled          *bool               `yaml:"network_disabled"`
	CPU                      string              `yaml:"cpu"`
	Memory                   string              `yaml:"memory"`
}) SandboxSpec {
	mode := strings.TrimSpace(item.Sandbox.Mode)
	if mode == "" {
		mode = defaultString(item.SandboxMode, "docker")
	}
	provider := strings.TrimSpace(item.Sandbox.Provider)
	if provider == "" {
		if strings.EqualFold(mode, "docker") {
			provider = "docker"
		} else {
			provider = "local"
		}
	}
	networkDisabled := true
	switch {
	case item.Sandbox.NetworkDisabled != nil:
		networkDisabled = *item.Sandbox.NetworkDisabled
	case item.NetworkDisabled != nil:
		networkDisabled = *item.NetworkDisabled
	}
	timeoutSeconds := item.Sandbox.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = item.TimeoutSeconds
	}
	image := strings.TrimSpace(item.Sandbox.Image)
	if image == "" {
		image = strings.TrimSpace(item.DockerImage)
	}
	images := normalizeStringMap(item.Sandbox.Images)
	if len(images) == 0 {
		images = normalizeStringMap(item.DockerImages)
	}
	cpu := strings.TrimSpace(item.Sandbox.CPU)
	if cpu == "" {
		cpu = strings.TrimSpace(item.CPU)
	}
	memory := strings.TrimSpace(item.Sandbox.Memory)
	if memory == "" {
		memory = strings.TrimSpace(item.Memory)
	}
	return SandboxSpec{
		Provider:        provider,
		Mode:            mode,
		Image:           image,
		Images:          images,
		TimeoutSeconds:  timeoutSeconds,
		NetworkDisabled: networkDisabled,
		CPU:             cpu,
		Memory:          memory,
	}
}

func normalizeSkills(cfg fileConfig, baseDir string) map[string][]contracts.SkillSpec {
	out := map[string][]contracts.SkillSpec{
		NoSkill: {{Name: NoSkill, Enabled: true, InjectMode: "none"}},
	}
	for name, item := range cfg.Skills {
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		if !enabled {
			continue
		}
		defaultVersion := strings.TrimSpace(item.DefaultVersion)
		if len(item.Versions) == 0 {
			version := defaultString(item.Version, "1")
			out[name] = []contracts.SkillSpec{buildSkillSpecVersion(name, version, defaultString(defaultVersion, version), item, skillVersionConfig{}, baseDir)}
			continue
		}
		versionNames := sortedKeys(item.Versions)
		if defaultVersion == "" {
			defaultVersion = versionNames[0]
		}
		for _, version := range versionNames {
			ver := item.Versions[version]
			verEnabled := true
			if ver.Enabled != nil {
				verEnabled = *ver.Enabled
			}
			if !verEnabled {
				continue
			}
			out[name] = append(out[name], buildSkillSpecVersion(name, version, defaultVersion, item, ver, baseDir))
		}
	}
	return out
}

func buildSkillSpecVersion(name, version, defaultVersion string, base skillConfig, ver skillVersionConfig, baseDir string) contracts.SkillSpec {
	description := firstNonEmptyString(ver.Description, base.Description)
	instructionPath := firstNonEmptyString(ver.InstructionPath, base.InstructionPath)
	files := ver.Files
	if len(files) == 0 {
		files = base.Files
	}
	resolvedFiles := make([]string, 0, len(files))
	for _, f := range files {
		resolvedFiles = append(resolvedFiles, resolveRelative(baseDir, f))
	}
	injectMode := firstNonEmptyString(ver.InjectMode, base.InjectMode)
	frameworks := ver.CompatibleFrameworks
	if len(frameworks) == 0 {
		frameworks = base.CompatibleFrameworks
	}
	langs := ver.CompatibleLanguages
	if len(langs) == 0 {
		langs = base.CompatibleLanguages
	}
	return contracts.SkillSpec{
		Name:                 name,
		Version:              version,
		DefaultVersion:       defaultVersion,
		Description:          description,
		InstructionPath:      resolveRelative(baseDir, instructionPath),
		Files:                resolvedFiles,
		InjectMode:           defaultString(injectMode, "prompt_append"),
		CompatibleFrameworks: frameworks,
		CompatibleLanguages:  langs,
		Enabled:              true,
	}
}

func defaultModelAPISubjects(models []string) []ResolvedSubject {
	out := make([]ResolvedSubject, 0, len(models))
	for _, model := range models {
		spec := contracts.SubjectSpec{
			ID:        SubjectID(KindModelAPI, model, NoSkill),
			Kind:      KindModelAPI,
			Framework: KindModelAPI,
			Model:     model,
			Skill:     NoSkill,
		}
		out = append(out, ResolvedSubject{
			Spec:      spec,
			Framework: FrameworkSpec{Name: KindModelAPI, Kind: KindModelAPI, Enabled: true},
			Skill:     contracts.SkillSpec{Name: NoSkill, Enabled: true, InjectMode: "none"},
		})
	}
	return out
}

func expandCartesian(models []string, frameworks map[string]FrameworkSpec, skills map[string][]contracts.SkillSpec) []ResolvedSubject {
	var out []ResolvedSubject
	frameworkNames := sortedKeys(frameworks)
	skillNames := sortedSkillKeys(skills)
	for _, frameworkName := range frameworkNames {
		fw := frameworks[frameworkName]
		if fw.Kind == KindModelAPI || fw.Name == KindModelAPI {
			continue
		}
		for _, model := range models {
			if !isCompatible(model, fw.CompatibleModels) {
				continue
			}
			for _, skillName := range skillNames {
				if skillName == NoSkill && fw.DisableNoSkill {
					continue
				}
				for _, skill := range skills[skillName] {
					if !isSkillCompatible(skill, fw.Name) {
						continue
					}
					out = append(out, buildResolvedSubject(fw, model, skill, nil, nil, ""))
				}
			}
		}
	}
	return out
}

func resolveSubjectEntry(entry SubjectEntry, frameworks map[string]FrameworkSpec, skills map[string][]contracts.SkillSpec) (ResolvedSubject, bool, error) {
	frameworkName := defaultString(entry.Framework, KindModelAPI)
	model := strings.TrimSpace(entry.Model)
	if model == "" {
		return ResolvedSubject{}, false, fmt.Errorf("subject %q missing model", entry.ID)
	}
	skillName := defaultString(entry.Skill, NoSkill)
	if frameworkName == KindModelAPI {
		return buildResolvedSubject(FrameworkSpec{Name: KindModelAPI, Kind: KindModelAPI, Enabled: true}, model, contracts.SkillSpec{Name: NoSkill, Enabled: true, InjectMode: "none"}, entry.Labels, entry.Tags, entry.ID), true, nil
	}
	fw, ok := frameworks[frameworkName]
	if !ok {
		return ResolvedSubject{}, false, fmt.Errorf("subject %q references unknown framework %q", entry.ID, frameworkName)
	}
	skillVersions, ok := skills[skillName]
	if !ok || len(skillVersions) == 0 {
		return ResolvedSubject{}, false, fmt.Errorf("subject %q references unknown skill %q", entry.ID, skillName)
	}
	skillVersion := defaultString(entry.SkillVersion, entry.Version)
	skill, err := resolveSkillVersion(skillName, skillVersion, skillVersions)
	if err != nil {
		return ResolvedSubject{}, false, fmt.Errorf("subject %q: %w", entry.ID, err)
	}
	return buildResolvedSubject(fw, model, skill, entry.Labels, entry.Tags, entry.ID), true, nil
}

func resolveSkillVersion(skillName, version string, versions []contracts.SkillSpec) (contracts.SkillSpec, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		if len(versions) == 1 {
			return versions[0], nil
		}
		names := make([]string, 0, len(versions))
		for _, item := range versions {
			names = append(names, item.Version)
		}
		sort.Strings(names)
		return contracts.SkillSpec{}, fmt.Errorf("skill %q has multiple versions; choose one of: %s", skillName, strings.Join(names, ","))
	}
	for _, item := range versions {
		if item.Version == version {
			return item, nil
		}
	}
	return contracts.SkillSpec{}, fmt.Errorf("skill %q version %q not found", skillName, version)
}

func buildResolvedSubject(fw FrameworkSpec, model string, skill contracts.SkillSpec, labels, tags []string, explicitID string) ResolvedSubject {
	if skill.Name == "" {
		skill = contracts.SkillSpec{Name: NoSkill, Enabled: true, InjectMode: "none"}
	}
	id := strings.TrimSpace(explicitID)
	if id == "" {
		id = SubjectID(fw.Name, model, skill.Name, skill.Version)
	}
	return ResolvedSubject{
		Spec: contracts.SubjectSpec{
			ID:           id,
			Kind:         fw.Kind,
			Framework:    fw.Name,
			Model:        model,
			Skill:        skill.Name,
			SkillVersion: skill.Version,
			Labels:       labels,
			Tags:         tags,
		},
		Framework: fw,
		Skill:     skill,
	}
}

func SubjectID(framework, model, skill string, version ...string) string {
	framework = defaultString(framework, KindModelAPI)
	skill = defaultString(skill, NoSkill)
	base := sanitize(framework) + "__" + sanitize(model) + "__" + sanitize(skill)
	if skill == NoSkill || len(version) == 0 || strings.TrimSpace(version[0]) == "" {
		return base
	}
	v := sanitize(version[0])
	if strings.HasPrefix(v, "v") {
		return base + "__" + v
	}
	return base + "__v" + v
}

func filterSubjects(subjects []ResolvedSubject, selected []string) ([]ResolvedSubject, error) {
	selected = uniqueNonEmpty(selected)
	if len(selected) == 0 {
		sort.Slice(subjects, func(i, j int) bool { return subjects[i].Spec.ID < subjects[j].Spec.ID })
		return subjects, nil
	}
	index := map[string][]ResolvedSubject{}
	for _, subject := range subjects {
		index[subject.Spec.ID] = append(index[subject.Spec.ID], subject)
		legacy := SubjectID(subject.Spec.Framework, subject.Spec.Model, subject.Spec.Skill)
		if legacy != subject.Spec.ID {
			index[legacy] = append(index[legacy], subject)
		}
	}
	var out []ResolvedSubject
	seen := map[string]struct{}{}
	var missing []string
	for _, id := range selected {
		matches := index[id]
		if len(matches) == 0 {
			missing = append(missing, id)
			continue
		}
		unique := uniqueSubjects(matches)
		if len(unique) > 1 {
			versions := make([]string, 0, len(unique))
			for _, subject := range unique {
				versions = append(versions, firstNonEmptyString(subject.Spec.SkillVersion, "unversioned"))
			}
			sort.Strings(versions)
			return nil, fmt.Errorf("selected subject %q is ambiguous across skill versions: %s", id, strings.Join(versions, ","))
		}
		subject := unique[0]
		if _, ok := seen[subject.Spec.ID]; ok {
			continue
		}
		seen[subject.Spec.ID] = struct{}{}
		out = append(out, subject)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("unknown selected subjects: %s", strings.Join(missing, ","))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec.ID < out[j].Spec.ID })
	return out, nil
}

func uniqueSubjects(subjects []ResolvedSubject) []ResolvedSubject {
	seen := map[string]struct{}{}
	out := make([]ResolvedSubject, 0, len(subjects))
	for _, subject := range subjects {
		if _, ok := seen[subject.Spec.ID]; ok {
			continue
		}
		seen[subject.Spec.ID] = struct{}{}
		out = append(out, subject)
	}
	return out
}

func dedupeSubjects(subjects []ResolvedSubject) []ResolvedSubject {
	seen := map[string]struct{}{}
	out := make([]ResolvedSubject, 0, len(subjects))
	for _, subject := range subjects {
		if subject.Spec.ID == "" {
			continue
		}
		if _, ok := seen[subject.Spec.ID]; ok {
			continue
		}
		seen[subject.Spec.ID] = struct{}{}
		out = append(out, subject)
	}
	return out
}

func isCompatible(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}

func isSkillCompatible(skill contracts.SkillSpec, framework string) bool {
	return isCompatible(framework, skill.CompatibleFrameworks)
}

func resolveRelative(baseDir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func normalizeStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeStringSliceMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cleaned := uniqueNonEmpty(values)
		if len(cleaned) == 0 {
			continue
		}
		out[key] = cleaned
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uniqueNonEmpty(items []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func intersect(left, right []string) []string {
	rightSet := map[string]struct{}{}
	for _, item := range right {
		rightSet[item] = struct{}{}
	}
	var out []string
	for _, item := range left {
		if _, ok := rightSet[item]; ok {
			out = append(out, item)
		}
	}
	return uniqueNonEmpty(out)
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedSkillKeys(m map[string][]contracts.SkillSpec) []string {
	return sortedKeys(m)
}

func sortedSet(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func sanitize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}
