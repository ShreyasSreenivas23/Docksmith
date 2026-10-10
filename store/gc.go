package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LayerReferences scans all manifests and returns digest -> reference count.
func LayerReferences() (map[string]int, error) {
	manifests, err := ListManifests()
	if err != nil {
		return nil, err
	}
	refs := make(map[string]int)
	for _, m := range manifests {
		for _, l := range m.Layers {
			refs[l.Digest]++
		}
	}
	return refs, nil
}

func digestFromLayerFilename(name string) string {
	if !strings.HasSuffix(name, ".tar") {
		return ""
	}
	base := strings.TrimSuffix(name, ".tar")
	return strings.Replace(base, "_", ":", 1)
}

// PruneOrphanLayers deletes layer tar files not referenced by any manifest.
func PruneOrphanLayers() (int, error) {
	if err := EnsureDirs(); err != nil {
		return 0, err
	}
	refs, err := LayerReferences()
	if err != nil {
		return 0, err
	}

	entries, err := os.ReadDir(LayersDir())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("list layers directory: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		digest := digestFromLayerFilename(entry.Name())
		if digest == "" {
			continue
		}
		if refs[digest] > 0 {
			continue
		}
		path := filepath.Join(LayersDir(), entry.Name())
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove orphan layer %s: %w", digest, err)
		}
		removed++
	}
	return removed, nil
}
