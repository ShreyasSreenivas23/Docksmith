package cache_test

import (
	"os"
	"path/filepath"
	"testing"

	"docksmith/cache"
	"docksmith/store"
)

func setupTestStore(t *testing.T) string {
	dir := t.TempDir()
	store.SetHomeForTest(dir)
	if err := store.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return dir
}

func TestComputeCacheKeyDeterministic(t *testing.T) {
	env := map[string]string{"PORT": "8080", "PATH": "/bin"}
	hashes := map[string]string{"app.txt": "abc123"}
	k1 := cache.ComputeCacheKey("sha256:prev", "COPY app.txt /app/", "/app", env, hashes)
	k2 := cache.ComputeCacheKey("sha256:prev", "COPY app.txt /app/", "/app", env, hashes)
	if k1 != k2 {
		t.Fatalf("cache keys should be deterministic: %s vs %s", k1, k2)
	}
	if !stringsHasPrefix(k1, "sha256:") {
		t.Fatalf("cache key should be sha256 digest: %s", k1)
	}
}

func TestComputeCacheKeyChangesWithInputs(t *testing.T) {
	base := cache.ComputeCacheKey("sha256:prev", "RUN echo hi", "/app", nil, nil)
	different := cache.ComputeCacheKey("sha256:other", "RUN echo hi", "/app", nil, nil)
	if base == different {
		t.Fatal("cache key should change when prev digest changes")
	}
}

func TestStoreAndLookup(t *testing.T) {
	setupTestStore(t)
	digest := "sha256:deadbeef"
	layerPath := store.LayerPath(digest)
	if err := os.WriteFile(layerPath, []byte("layer-data"), 0644); err != nil {
		t.Fatalf("write layer: %v", err)
	}

	key := "sha256:cachekey"
	if err := cache.Store(key, digest); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got := cache.Lookup(key)
	if got != digest {
		t.Fatalf("Lookup returned %s, want %s", got, digest)
	}
}

func TestLookupMissingLayerReturnsEmpty(t *testing.T) {
	setupTestStore(t)
	key := "sha256:missing"
	if err := cache.Store(key, "sha256:noton disk"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got := cache.Lookup(key); got != "" {
		t.Fatalf("Lookup should return empty for missing layer, got %s", got)
	}
}

func TestPruneStaleRemovesOrphanedEntries(t *testing.T) {
	setupTestStore(t)
	key := "sha256:stale"
	digest := "sha256:gone"
	if err := cache.Store(key, digest); err != nil {
		t.Fatalf("Store: %v", err)
	}
	removed, err := cache.PruneStale()
	if err != nil {
		t.Fatalf("PruneStale: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 stale entry removed, got %d", removed)
	}
	if cache.Lookup(key) != "" {
		t.Fatal("stale cache entry should be gone")
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	content := []byte("docksmith-cache-test")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	hash, err := cache.HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if len(hash) != 64 {
		t.Fatalf("expected 64-char hex hash, got %s", hash)
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
