package layer_test

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"docksmith/layer"
	"docksmith/store"
)

func layerDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}

func buildTar(files map[string]string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Size: int64(len(content)), Mode: 0644}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if len(content) > 0 {
			if _, err := tw.Write([]byte(content)); err != nil {
				panic(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func storeAndExtract(target string, tarBytes []byte) error {
	home := filepath.Join(target, ".docksmith-home")
	store.SetHomeForTest(home)
	if err := store.EnsureDirs(); err != nil {
		return err
	}
	digest := layerDigest(tarBytes)
	if err := layer.StoreTar(tarBytes, digest); err != nil {
		return err
	}
	return layer.ExtractLayers(target, []string{digest})
}

func tarContainsName(tarBytes []byte, name string) bool {
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		hdr, err := tr.Next()
		if err != nil {
			return false
		}
		if hdr.Name == name {
			return true
		}
	}
}

func TestWhiteoutHelpers(t *testing.T) {
	got := layer.WhiteoutPath("foo/bar.txt")
	if got != "foo/.wh.bar.txt" {
		t.Fatalf("WhiteoutPath = %s", got)
	}
	if layer.HiddenPathFromWhiteout("foo/.wh.bar.txt") != "foo/bar.txt" {
		t.Fatal("HiddenPathFromWhiteout mismatch")
	}
	if !layer.IsOpaqueWhiteout("dir/.wh..wh..opq") {
		t.Fatal("expected opaque whiteout")
	}
}

func TestCreateCopyLayer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	tarBytes, digest, err := layer.CreateCopyLayer(dir, "hello.txt", "hello.txt", "/app")
	if err != nil {
		t.Fatalf("CreateCopyLayer: %v", err)
	}
	if len(tarBytes) == 0 || digest == "" {
		t.Fatal("expected non-empty layer")
	}

	extractDir := t.TempDir()
	if err := storeAndExtract(extractDir, tarBytes); err != nil {
		t.Fatalf("extract: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(extractDir, "app", "hello.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestCreateCopyLayerGlob(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, err := layer.CreateCopyLayer(dir, "**/*.txt", "/all/", "")
	if err != nil {
		t.Fatalf("CreateCopyLayer glob: %v", err)
	}
}

func TestCreateRunLayerDetectsDeletions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatalf("write keep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "remove.txt"), []byte("gone"), 0644); err != nil {
		t.Fatalf("write remove: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "removed-dir"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	before, err := layer.SnapshotDir(root)
	if err != nil {
		t.Fatalf("SnapshotDir: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "remove.txt")); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "removed-dir")); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	tarBytes, _, err := layer.CreateRunLayer(root, before)
	if err != nil {
		t.Fatalf("CreateRunLayer: %v", err)
	}
	if !tarContainsName(tarBytes, layer.WhiteoutPath("remove.txt")) {
		t.Fatal("expected file whiteout for remove.txt")
	}
	if !tarContainsName(tarBytes, layer.OpaqueDirWhiteoutPath("removed-dir")) {
		t.Fatal("expected opaque whiteout for removed-dir")
	}
}

func TestExtractLayersAppliesWhiteoutsAcrossLayers(t *testing.T) {
	home := t.TempDir()
	store.SetHomeForTest(home)
	if err := store.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	baseTar := buildTar(map[string]string{
		"data/old.txt": "old",
		"data/new.txt": "new",
	})
	baseDigest := layerDigest(baseTar)
	if err := layer.StoreTar(baseTar, baseDigest); err != nil {
		t.Fatalf("store base: %v", err)
	}

	whiteoutTar := buildTar(map[string]string{
		layer.WhiteoutPath("data/old.txt"): "",
	})
	whiteoutDigest := layerDigest(whiteoutTar)
	if err := layer.StoreTar(whiteoutTar, whiteoutDigest); err != nil {
		t.Fatalf("store whiteout: %v", err)
	}

	target := t.TempDir()
	if err := layer.ExtractLayers(target, []string{baseDigest, whiteoutDigest}); err != nil {
		t.Fatalf("ExtractLayers: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "old.txt")); !os.IsNotExist(err) {
		t.Fatal("old.txt should be hidden by whiteout")
	}
	if _, err := os.Stat(filepath.Join(target, "data", "new.txt")); err != nil {
		t.Fatalf("new.txt should exist: %v", err)
	}
}

func TestSnapshotDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap, err := layer.SnapshotDir(root)
	if err != nil {
		t.Fatalf("SnapshotDir: %v", err)
	}
	if _, ok := snap["file.txt"]; !ok {
		t.Fatal("snapshot missing file.txt")
	}
}

func TestStoreTarIsImmutable(t *testing.T) {
	home := t.TempDir()
	store.SetHomeForTest(home)
	if err := store.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	data := []byte("immutable-layer")
	digest := layerDigest(data)
	if err := layer.StoreTar(data, digest); err != nil {
		t.Fatalf("StoreTar: %v", err)
	}
	if err := layer.StoreTar([]byte("changed"), digest); err != nil {
		t.Fatalf("StoreTar second write: %v", err)
	}
	got, err := os.ReadFile(store.LayerPath(digest))
	if err != nil {
		t.Fatalf("read layer: %v", err)
	}
	if string(got) != "immutable-layer" {
		t.Fatalf("layer was overwritten: %q", string(got))
	}
}
