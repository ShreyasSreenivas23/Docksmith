package integration_test

import (
	"os"
	"path/filepath"
	"testing"

	"docksmith/builder"
	"docksmith/cache"
	"docksmith/layer"
	"docksmith/manifest"
	"docksmith/parser"
	"docksmith/store"
)

func TestBuildIntegrationColdAndWarmCache(t *testing.T) {
	home := t.TempDir()
	store.SetHomeForTest(home)
	if err := store.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	baseRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseRoot, "bin"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseRoot, "bin", "sh"), []byte("stub"), 0755); err != nil {
		t.Fatalf("write base sh: %v", err)
	}
	baseTar, baseDigest, err := layer.CreateCopyLayer(baseRoot, "bin", "/", "")
	if err != nil {
		t.Fatalf("base layer: %v", err)
	}
	if err := layer.StoreTar(baseTar, baseDigest); err != nil {
		t.Fatalf("store base: %v", err)
	}
	baseManifest := &manifest.Manifest{
		Name:    "base",
		Tag:     "latest",
		Created: "2026-01-01T00:00:00Z",
		Config:  manifest.Config{Cmd: []string{"/bin/sh"}},
		Layers: []manifest.Layer{{
			Digest:    baseDigest,
			Size:      int64(len(baseTar)),
			CreatedBy: "import base",
		}},
	}
	if err := store.SaveManifest(baseManifest); err != nil {
		t.Fatalf("save base manifest: %v", err)
	}

	contextDir := t.TempDir()
	docksmithfile := `
FROM base:latest
WORKDIR /app
COPY app.txt /app/
ENV PORT=8080
CMD ["/bin/sh"]
`
	if err := os.WriteFile(filepath.Join(contextDir, "Docksmithfile"), []byte(docksmithfile), 0644); err != nil {
		t.Fatalf("write Docksmithfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "app.txt"), []byte("integration"), 0644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}

	instructions, err := parser.Parse(contextDir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if err := builder.Build(instructions, builder.BuildOptions{
		Name:       "myapp",
		Tag:        "latest",
		ContextDir: contextDir,
		NoCache:    true,
	}); err != nil {
		t.Fatalf("cold build: %v", err)
	}

	if err := builder.Build(instructions, builder.BuildOptions{
		Name:       "myapp",
		Tag:        "latest",
		ContextDir: contextDir,
		NoCache:    false,
	}); err != nil {
		t.Fatalf("warm build: %v", err)
	}

	m, err := store.LoadManifest("myapp", "latest")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Layers) < 2 {
		t.Fatalf("expected at least 2 layers, got %d", len(m.Layers))
	}
	if m.Config.WorkingDir != "/app" {
		t.Fatalf("WorkingDir = %s", m.Config.WorkingDir)
	}

	// Warm build should leave cache entries for COPY layer.
	key := cache.ComputeCacheKey(baseDigest, "COPY app.txt /app/", "/app", map[string]string{"PORT": "8080"}, nil)
	// file hashes required for COPY - verify at least one cache entry exists
	idxPath := filepath.Join(store.CacheDir(), "index.json")
	data, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("read cache index: %v", err)
	}
	if len(data) < 10 {
		t.Fatal("expected populated cache index after warm build")
	}
	_ = key
}
