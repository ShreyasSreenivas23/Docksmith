package cache_test

import (
	"testing"

	"docksmith/cache"
)

func BenchmarkComputeCacheKey(b *testing.B) {
	env := map[string]string{"PORT": "8080", "PATH": "/bin"}
	hashes := map[string]string{"app.txt": "abc123", "lib.go": "def456"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.ComputeCacheKey("sha256:prev", "COPY app.txt /app/", "/app", env, hashes)
	}
}
