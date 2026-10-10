package layer_test

import (
	"os"
	"path/filepath"
	"testing"

	"docksmith/layer"
	"docksmith/store"
)

func BenchmarkCreateCopyLayer(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 100; i++ {
		name := filepath.Join(dir, "file"+string(rune('a'+i%26))+".txt")
		if err := os.WriteFile(name, []byte("payload"), 0644); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := layer.CreateCopyLayer(dir, "*.txt", "/dest/", ""); err != nil {
			b.Fatalf("CreateCopyLayer: %v", err)
		}
	}
}

func BenchmarkSnapshotDir(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 100; i++ {
		if err := os.WriteFile(filepath.Join(root, "f"+string(rune('a'+i%26))+".txt"), []byte("x"), 0644); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := layer.SnapshotDir(root); err != nil {
			b.Fatalf("SnapshotDir: %v", err)
		}
	}
}

func BenchmarkExtractLayers(b *testing.B) {
	home := b.TempDir()
	store.SetHomeForTest(home)
	if err := store.EnsureDirs(); err != nil {
		b.Fatalf("EnsureDirs: %v", err)
	}

	tarBytes := buildTar(map[string]string{
		"a.txt": "a",
		"b.txt": "b",
		"c.txt": "c",
	})
	digest := layerDigest(tarBytes)
	if err := layer.StoreTar(tarBytes, digest); err != nil {
		b.Fatalf("StoreTar: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		if err := layer.ExtractLayers(dir, []string{digest}); err != nil {
			b.Fatalf("ExtractLayers: %v", err)
		}
	}
}
