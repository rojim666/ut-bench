package web

import (
	"fmt"
	pathpkg "path"
	"path/filepath"
	"strings"

	"go-ut-bench/internal/contracts"
)

func (m *RunManager) prepareDockerGeneratedManifest(spec contracts.RunSpec) (string, error) {
	source := filepath.Join(spec.OutputRoot, "runs", spec.RunID, "generated", "generated_manifest.json")
	target := filepath.Join(spec.OutputRoot, "runs", spec.RunID, "generated", "generated_manifest.docker.json")
	return m.writeDockerGeneratedManifest(spec, source, target)
}

func (m *RunManager) writeDockerGeneratedManifest(spec contracts.RunSpec, source, target string) (string, error) {
	manifest, err := contracts.ReadGeneratedManifest(source)
	if err != nil {
		return "", err
	}

	root := strings.TrimRight(m.dockerCfg.ProjectRoot, `/\`)
	mapper := newDockerPathMapper(root, spec.DatasetRoot)
	manifest.Spec.DatasetRoot = "/app/datasets"
	manifest.Spec.OutputRoot = "/app/artifacts"
	manifest.Spec.ConfigPath = "/app/configs/models.yaml"
	if strings.TrimSpace(manifest.Spec.AgentsConfigPath) != "" {
		manifest.Spec.AgentsConfigPath = pathpkg.Join("/app/configs", filepath.Base(manifest.Spec.AgentsConfigPath))
	}
	if strings.TrimSpace(manifest.Spec.DBPath) != "" {
		manifest.Spec.DBPath = "/app/storage/utbench.db"
	}
	manifest.PromptSnapshotDir = mapper(manifest.PromptSnapshotDir)
	for i := range manifest.Cases {
		c := &manifest.Cases[i]
		c.SamplePath = mapper(c.SamplePath)
		c.PromptPath = mapper(c.PromptPath)
		c.GeneratedTestPath = mapper(c.GeneratedTestPath)
		c.ResponsePath = mapper(c.ResponsePath)
		c.MetadataPath = mapper(c.MetadataPath)
		c.TracePath = mapper(c.TracePath)
		c.WorkspaceDiffPath = mapper(c.WorkspaceDiffPath)
		c.FileModule.WorkspaceRoot = mapper(c.FileModule.WorkspaceRoot)
		c.FileModule.TargetFileAbs = mapper(c.FileModule.TargetFileAbs)
		c.FileModule.GeneratedTestPath = mapper(c.FileModule.GeneratedTestPath)
	}

	if err := contracts.WriteJSON(target, manifest); err != nil {
		return "", fmt.Errorf("write docker manifest: %w", err)
	}
	return target, nil
}

func newDockerPathMapper(projectRoot, datasetRoot string) func(string) string {
	root := filepath.ToSlash(filepath.Clean(projectRoot))
	hostDataset := resolveDockerHostPath(root, datasetRoot, "datasets")
	mounts := []struct {
		host      string
		container string
	}{
		{host: hostDataset, container: "/app/datasets"},
		{host: filepath.ToSlash(filepath.Join(root, "artifacts")), container: "/app/artifacts"},
		{host: filepath.ToSlash(filepath.Join(root, "configs")), container: "/app/configs"},
		{host: filepath.ToSlash(filepath.Join(root, "storage")), container: "/app/storage"},
	}

	return func(value string) string {
		raw := strings.TrimSpace(value)
		if raw == "" {
			return value
		}
		slashRaw := filepath.ToSlash(raw)
		if strings.HasPrefix(slashRaw, "/app/") {
			return pathpkg.Clean(slashRaw)
		}

		clean := filepath.Clean(raw)
		if !filepath.IsAbs(clean) {
			if strings.HasPrefix(filepath.ToSlash(clean), "artifacts/") {
				clean = filepath.Join(root, clean)
			} else {
				abs, err := filepath.Abs(clean)
				if err == nil {
					clean = abs
				}
			}
		}

		slash := filepath.ToSlash(filepath.Clean(clean))
		for _, mount := range mounts {
			host := strings.TrimRight(filepath.ToSlash(filepath.Clean(mount.host)), "/")
			if slash == host {
				return mount.container
			}
			if strings.HasPrefix(slash, host+"/") {
				return pathpkg.Join(mount.container, slash[len(host)+1:])
			}
		}
		return value
	}
}
