package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"docksmith/manifest"
	"docksmith/store"
)

func setupStore(t *testing.T) {
	store.SetHomeForTest(t.TempDir())
	if err := store.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
}

func writeLayer(t *testing.T, digest string, data []byte) {
	path := store.LayerPath(digest)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write layer: %v", err)
	}
}

func saveImage(t *testing.T, name, tag string, layers []string) {
	var manifestLayers []manifest.Layer
	for _, d := range layers {
		manifestLayers = append(manifestLayers, manifest.Layer{
			Digest:    d,
			Size:      int64(len(d)),
			CreatedBy: "test",
		})
	}
	m := &manifest.Manifest{
		Name:    name,
		Tag:     tag,
		Created: "2026-01-01T00:00:00Z",
		Config:  manifest.Config{Cmd: []string{"/bin/sh"}},
		Layers:  manifestLayers,
	}
	if err := store.SaveManifest(m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
}

func TestLayerReferencesCountsSharedLayers(t *testing.T) {
	setupStore(t)
	shared := "sha256:sharedlayer"
	writeLayer(t, shared, []byte("shared"))
	saveImage(t, "img1", "latest", []string{shared, "sha256:only1"})
	saveImage(t, "img2", "latest", []string{shared, "sha256:only2"})

	refs, err := store.LayerReferences()
	if err != nil {
		t.Fatalf("LayerReferences: %v", err)
	}
	if refs[shared] != 2 {
		t.Fatalf("shared layer refs = %d, want 2", refs[shared])
	}
}

func TestRemoveImageKeepsSharedLayers(t *testing.T) {
	setupStore(t)
	shared := "sha256:sharedlayer"
	only1 := "sha256:only1"
	writeLayer(t, shared, []byte("shared"))
	writeLayer(t, only1, []byte("only1"))
	saveImage(t, "img1", "latest", []string{shared, only1})
	saveImage(t, "img2", "latest", []string{shared})

	if err := store.RemoveImage("img1", "latest"); err != nil {
		t.Fatalf("RemoveImage: %v", err)
	}
	if _, err := os.Stat(store.LayerPath(shared)); err != nil {
		t.Fatal("shared layer should remain")
	}
	if _, err := os.Stat(store.LayerPath(only1)); !os.IsNotExist(err) {
		t.Fatal("unreferenced only1 layer should be removed")
	}
}

func TestPruneOrphanLayers(t *testing.T) {
	setupStore(t)
	used := "sha256:used"
	orphan := "sha256:orphan"
	writeLayer(t, used, []byte("used"))
	writeLayer(t, orphan, []byte("orphan"))
	saveImage(t, "app", "latest", []string{used})

	removed, err := store.PruneOrphanLayers()
	if err != nil {
		t.Fatalf("PruneOrphanLayers: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(store.LayerPath(orphan)); !os.IsNotExist(err) {
		t.Fatal("orphan layer should be deleted")
	}
	if _, err := os.Stat(store.LayerPath(used)); err != nil {
		t.Fatal("used layer should remain")
	}
}

func TestSaveAndLoadManifest(t *testing.T) {
	setupStore(t)
	m := &manifest.Manifest{
		Name:    "myapp",
		Tag:     "v1",
		Created: "2026-01-01T00:00:00Z",
		Config: manifest.Config{
			WorkingDir: "/app",
			Env:        []string{"PORT=8080"},
			Cmd:        []string{"/app/server"},
		},
		Layers: []manifest.Layer{{
			Digest:    "sha256:abc",
			Size:      10,
			CreatedBy: "FROM alpine",
		}},
	}
	if err := store.SaveManifest(m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	got, err := store.LoadManifest("myapp", "v1")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got.Config.WorkingDir != "/app" || got.Digest == "" {
		t.Fatalf("unexpected manifest: %+v", got)
	}
	path := filepath.Join(store.ImagesDir(), "myapp_v1.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("manifest file missing: %v", err)
	}
}
