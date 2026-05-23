// dataset/manifest.go 提供数据集清单加载能力。
// 从 JSON 文件解析样本列表，并将相对路径规范化为绝对路径。
package dataset

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"go-ut-bench/internal/contracts"
)

// datasetManifest 描述数据集清单文件结构。
type datasetManifest struct {
	Level   string                  `json:"level"`
	Samples []datasetManifestSample `json:"samples"`
}

// datasetManifestSample 描述清单中的单个样本。
type datasetManifestSample struct {
	ID       string                 `json:"id"`
	Language string                 `json:"language"`
	Category contracts.DatasetClass `json:"category"`
	Scenario string                 `json:"scenario"`
	Path     string                 `json:"path"`
}

// loadManifest 加载并解析数据集清单文件。
func loadManifest(path string, datasetRoot string) (datasetManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return datasetManifest{}, err
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	var mf datasetManifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		return datasetManifest{}, err
	}
	for i := range mf.Samples {
		sample := &mf.Samples[i]
		if sample.Path == "" {
			sample.Path = filepath.Join(sample.Language, sample.ID+languageExtByName(sample.Language))
		}
		if !filepath.IsAbs(sample.Path) {
			candidate := filepath.Clean(sample.Path)
			if _, err := os.Stat(candidate); err == nil {
				sample.Path = absoluteCleanPath(candidate)
			} else {
				sample.Path = filepath.Join(datasetRoot, sample.Path)
			}
		}
		sample.Path = absoluteCleanPath(sample.Path)
		sample.Language = strings.ToLower(strings.TrimSpace(sample.Language))
		sample.Scenario = strings.TrimSpace(sample.Scenario)
	}
	return mf, nil
}
