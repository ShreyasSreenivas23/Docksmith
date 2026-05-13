package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"docksmith/manifest"
)

// DocksmithDir returns the path to ~/.docksmith/.
func DocksmithDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}
	return filepath.Join(home, ".docksmith")
}

// ImagesDir returns ~/.docksmith/images/.
func ImagesDir() string {
	return filepath.Join(DocksmithDir(), "images")
}

// LayersDir returns ~/.docksmith/layers/.
func LayersDir() string {
	return filepath.Join(DocksmithDir(), "layers")
}

// CacheDir returns ~/.docksmith/cache/.
func CacheDir() string {
	return filepath.Join(DocksmithDir(), "cache")
}

// EnsureDirs creates the required directory structure.
func EnsureDirs() error {
	dirs := []string{ImagesDir(), LayersDir(), CacheDir()}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", d, err)
		}
	}
	return nil
}

// manifestFilename returns the filename for a manifest.
func manifestFilename(name, tag string) string {
	safeName := strings.ReplaceAll(name, "/", "_")
	safeName = strings.ReplaceAll(safeName, ":", "_")
	safeTag := strings.ReplaceAll(tag, "/", "_")
	safeTag = strings.ReplaceAll(safeTag, ":", "_")
	return safeName + "_" + safeTag + ".json"
}

// SaveManifest writes a manifest to disk, computing its digest first.
func SaveManifest(m *manifest.Manifest) error {
	if err := EnsureDirs(); err != nil {
		return err
	}
	if err := manifest.ComputeDigest(m); err != nil {
		return err
	}
	data, err := manifest.Serialize(m)
	if err != nil {
		return err
	}
	path := filepath.Join(ImagesDir(), manifestFilename(m.Name, m.Tag))
	return os.WriteFile(path, data, 0644)
}

// LoadManifest reads a manifest by name:tag.
func LoadManifest(name, tag string) (*manifest.Manifest, error) {
	path := filepath.Join(ImagesDir(), manifestFilename(name, tag))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("image %s:%s not found", name, tag)
		}
		return nil, fmt.Errorf("read manifest %s:%s: %w", name, tag, err)
	}
	return manifest.Deserialize(data)
}

// ListManifests returns all manifests in the images directory.
func ListManifests() ([]*manifest.Manifest, error) {
	dir := ImagesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list images directory: %w", err)
	}
	var manifests []*manifest.Manifest
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		m, err := manifest.Deserialize(data)
		if err != nil {
			continue
		}
		manifests = append(manifests, m)
	}
	return manifests, nil
}

// RemoveImage deletes the manifest and all its layer files.
func RemoveImage(name, tag string) error {
	m, err := LoadManifest(name, tag)
	if err != nil {
		return err
	}
	for _, l := range m.Layers {
		layerPath := LayerPath(l.Digest)
		if err := os.Remove(layerPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: could not remove layer %s: %v\n", l.Digest, err)
		}
	}
	manifestPath := filepath.Join(ImagesDir(), manifestFilename(name, tag))
	return os.Remove(manifestPath)
}

// LayerPath returns the full path to a layer tar file.
func LayerPath(digest string) string {
	safe := strings.ReplaceAll(digest, ":", "_")
	return filepath.Join(LayersDir(), safe+".tar")
}

// LayerExists checks if a layer tar exists on disk.
func LayerExists(digest string) bool {
	_, err := os.Stat(LayerPath(digest))
	return err == nil
}
