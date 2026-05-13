package manifest
// Manifest is the "source of truth" that describes what an image is made of,
// how it should be configured, and where its actual data layers lives.
import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Layer represents a single image layer.
type Layer struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	CreatedBy string `json:"createdBy"`
}

// Config holds image configuration.
type Config struct {
	Env        []string `json:"Env"`
	Cmd        []string `json:"Cmd"`
	WorkingDir string   `json:"WorkingDir"`
}

// Manifest is the top-level image manifest.
type Manifest struct {
	Name    string  `json:"name"`
	Tag     string  `json:"tag"`
	Digest  string  `json:"digest"`
	Created string  `json:"created"`
	Config  Config  `json:"config"`
	Layers  []Layer `json:"layers"`
}

// ComputeDigest computes the SHA-256 digest of the manifest.
// It serialises the manifest with digest set to "", hashes those bytes,
// then sets the computed digest.
func ComputeDigest(m *Manifest) error {
	m.Digest = ""
	canonical, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest for digest: %w", err)
	}
	hash := sha256.Sum256(canonical)
	m.Digest = fmt.Sprintf("sha256:%x", hash)
	return nil
}

// Serialize returns the JSON representation of the manifest.
func Serialize(m *Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize manifest: %w", err)
	}
	return data, nil
}

// Deserialize parses a manifest from JSON bytes.
func Deserialize(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("deserialize manifest: %w", err)
	}
	return &m, nil
}

// ShortID returns the first 12 hex characters of the digest (after "sha256:").
func ShortID(m *Manifest) string {
	if len(m.Digest) > 19 {
		return m.Digest[7:19]
	}
	return m.Digest
}
