package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"docksmith/store"
)

// Index maps cache keys to layer digests.
type Index struct {
	Entries map[string]string `json:"entries"`
}

var mu sync.Mutex

func indexPath() string {
	return filepath.Join(store.CacheDir(), "index.json")
}

func loadIndex() (*Index, error) {
	data, err := os.ReadFile(indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &Index{Entries: make(map[string]string)}, nil
		}
		return nil, fmt.Errorf("read cache index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return &Index{Entries: make(map[string]string)}, nil
	}
	if idx.Entries == nil {
		idx.Entries = make(map[string]string)
	}
	return &idx, nil
}

func saveIndex(idx *Index) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache index: %w", err)
	}
	return os.WriteFile(indexPath(), data, 0644)
}

// Lookup checks if a cache key exists and the referenced layer is on disk.
func Lookup(key string) string {
	mu.Lock()
	defer mu.Unlock()

	idx, err := loadIndex()
	if err != nil {
		return ""
	}
	digest, ok := idx.Entries[key]
	if !ok {
		return ""
	}
	if !store.LayerExists(digest) {
		return ""
	}
	return digest
}

// Store saves a cache key -> layer digest mapping.
func Store(key, digest string) error {
	mu.Lock()
	defer mu.Unlock()

	if err := store.EnsureDirs(); err != nil {
		return err
	}
	idx, err := loadIndex()
	if err != nil {
		return err
	}
	idx.Entries[key] = digest
	return saveIndex(idx)
}

// ComputeCacheKey computes a deterministic cache key for a layer-producing instruction.
//
// key = SHA-256(prevDigest + instructionText + workdir + envState [+ fileHashes for COPY])
func ComputeCacheKey(prevDigest, instructionText, workdir string, envState map[string]string, fileHashes map[string]string) string {
	h := sha256.New()
	h.Write([]byte(prevDigest))
	h.Write([]byte(instructionText))
	h.Write([]byte(workdir))
	h.Write([]byte(serializeEnvState(envState)))
	if fileHashes != nil {
		h.Write([]byte(serializeFileHashes(fileHashes)))
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

func serializeEnvState(envState map[string]string) string {
	if len(envState) == 0 {
		return ""
	}
	keys := make([]string, 0, len(envState))
	for k := range envState {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+envState[k])
	}
	return strings.Join(parts, "\n")
}

func serializeFileHashes(fileHashes map[string]string) string {
	if len(fileHashes) == 0 {
		return ""
	}
	paths := make([]string, 0, len(fileHashes))
	for p := range fileHashes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var parts []string
	for _, p := range paths {
		parts = append(parts, fileHashes[p])
	}
	return strings.Join(parts, "")
}

// HashFile computes the SHA-256 hex digest of a file's contents.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file for hashing: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
