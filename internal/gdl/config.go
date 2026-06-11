package gdl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/leqwin/monloader/internal/config"
)

// WriteManagedConfig writes the gallery-dl config file the app controls: the
// download-archive, the metadata postprocessor that writes `<file>.json`
// sidecars, and per-site blocks. The download location is set per run with
// `-D <workDir>`, so none is written here. flatTagSites lists the categories
// that need `tags: true` to emit per-category tags. The validated raw passthrough
// is deep-merged last so operator options win; the file is rewritten from config,
// never hand-edited.
func WriteManagedConfig(cfg *config.Config, flatTagSites []string) error {
	extractor := map[string]any{
		"directory": []any{},
		"postprocessors": []any{
			map[string]any{"name": "metadata", "mode": "json"},
		},
		// directlink's filename template is "{domain}/{path}/{filename}.{extension}";
		// with the directory flattened to the per-job workdir it folds into one
		// name, which a long URL pushes past the filesystem's 255-byte limit. Keep
		// just the file's own name so the download still lands.
		"directlink": map[string]any{"filename": "{filename}.{extension}"},
		// Cap an unreachable host; without these the worker blocks on gallery-dl's
		// long default timeout and retries. Overridable via raw_config.
		"timeout": 20.0,
		"retries": 2,
	}
	if cfg.GalleryDL.ArchivePath != "" {
		extractor["archive"] = cfg.GalleryDL.ArchivePath
	}
	if cfg.GalleryDL.SleepRequest > 0 {
		// sleep-request throttles extractor (listing) requests; sleep throttles
		// each file download, so a page of many files is not fetched in a burst.
		extractor["sleep-request"] = cfg.GalleryDL.SleepRequest
		extractor["sleep"] = cfg.GalleryDL.SleepRequest
	}

	// Every flat-tag family gets tags:true so its per-category tags appear,
	// even when the operator has not added credentials for it.
	for _, site := range flatTagSites {
		extractor[site] = map[string]any{"tags": true}
	}

	// Overlay per-site credentials, keeping any tags:true already set.
	for _, site := range cfg.Sites {
		block, _ := extractor[site.Name].(map[string]any)
		if block == nil {
			block = map[string]any{}
		}
		if site.Username != "" {
			block["username"] = site.Username
		}
		if site.APIKey != "" {
			block["api-key"] = site.APIKey
		}
		if site.UserID != "" {
			block["user-id"] = site.UserID
		}
		if site.Cookies != "" {
			block["cookies"] = site.Cookies
		}
		if slices.Contains(flatTagSites, site.Name) {
			block["tags"] = true
		}
		if len(block) > 0 {
			extractor[site.Name] = block
		}
	}

	managed := map[string]any{"extractor": extractor}

	if raw := cfg.GalleryDL.RawConfig; raw != "" {
		var rawMap map[string]any
		if err := json.Unmarshal([]byte(raw), &rawMap); err != nil {
			return fmt.Errorf("raw gallery-dl config is not valid JSON: %w", err)
		}
		mergeMaps(managed, rawMap)
	}

	data, err := json.MarshalIndent(managed, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding managed config: %w", err)
	}
	path := cfg.GalleryDL.ConfigPath
	if path == "" {
		return fmt.Errorf("gallerydl.config_path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating gallery-dl config dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing managed config: %w", err)
	}
	return nil
}

// mergeMaps deep-merges src into dst: nested maps merge recursively, and a
// scalar or array in src overwrites dst. Operator (src) values win.
func mergeMaps(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeMaps(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}
