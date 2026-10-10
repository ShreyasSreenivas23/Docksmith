package manifest_test

import (
	"testing"

	"docksmith/manifest"
)

func TestComputeDigestAndSerialize(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "app",
		Tag:     "latest",
		Created: "2026-01-01T00:00:00Z",
		Config: manifest.Config{
			WorkingDir: "/app",
			Env:        []string{"PORT=8080"},
			Cmd:        []string{"/bin/sh"},
		},
		Layers: []manifest.Layer{{
			Digest:    "sha256:layer1",
			Size:      42,
			CreatedBy: "COPY . /app",
		}},
	}
	if err := manifest.ComputeDigest(m); err != nil {
		t.Fatalf("ComputeDigest: %v", err)
	}
	if m.Digest == "" {
		t.Fatal("digest should be set")
	}

	data, err := manifest.Serialize(m)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	roundTrip, err := manifest.Deserialize(data)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}
	if roundTrip.Name != m.Name || roundTrip.Digest != m.Digest {
		t.Fatalf("round trip mismatch: %+v", roundTrip)
	}
}

func TestShortID(t *testing.T) {
	m := &manifest.Manifest{Digest: "sha256:abcdef0123456789"}
	got := manifest.ShortID(m)
	if got != "abcdef012345" {
		t.Fatalf("ShortID = %s", got)
	}
}
