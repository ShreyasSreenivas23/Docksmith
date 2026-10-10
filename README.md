# Docksmith

A simplified Docker-like build and runtime system built from scratch in Go.

## What is Docksmith?

Docksmith is a container build and runtime system that implements the core ideas behind Docker images, layers, and isolation — without external dependencies.

| Component | What it does |
|-----------|--------------|
| **Build system** | Reads a `Docksmithfile` with 6 instructions: `FROM`, `COPY`, `RUN`, `WORKDIR`, `ENV`, `CMD`. Each `COPY` and `RUN` produces an immutable, content-addressed layer stored as a tar file. |
| **Build cache** | Deterministic SHA-256 keys from previous layer digest, instruction text, `WORKDIR`, `ENV` state, and (for `COPY`) file content hashes. Cache misses cascade to all subsequent steps. |
| **Layer whiteouts** | `RUN` layers track file deletions via Docker-style whiteout entries (`.wh.*` and opaque-directory markers). Deleted files stay deleted when layers are assembled. |
| **Layer GC** | Reference-counted layer storage. `rmi` only deletes layers no longer referenced by any image. `prune` removes orphan layers and stale cache entries. |
| **Container runtime** | Linux isolation via `chroot` + namespaces (PID, mount, UTS, network, user). Capability dropping and `PR_SET_NO_NEW_PRIVS` run in the child process. The same primitive powers both `RUN` during build and `docksmith run`. |

## Prerequisites

- **Linux** (or WSL2 on Windows)
- **Go 1.21+**
- **Root privileges** (needed for chroot/namespace syscalls)
- **Docker** (optional — only for `scripts/setup-base-image.sh` and benchmark comparison)

## Quick Start

### 1. Build the binary

```bash
cd ~/docksmith
go build -o docksmith .
```

### 2. Import a base image

```bash
chmod +x scripts/setup-base-image.sh
sudo ./scripts/setup-base-image.sh
```

### 3. Build the sample app

```bash
sudo ./docksmith build -t myapp:latest ./sample-app
```

### 4. Run the container

```bash
sudo ./docksmith run myapp:latest
```

### 5. Inspect the image

```bash
sudo ./docksmith inspect myapp:latest
```

### 6. Test ENV override

```bash
sudo ./docksmith run -e GREETING=Goodbye myapp:latest
```

## CLI Reference

| Command | Description |
|---------|-------------|
| `docksmith build -t <name:tag> [--no-cache] <context>` | Build an image from a Docksmithfile |
| `docksmith run [-e KEY=VALUE]... <name:tag> [cmd...]` | Run a container from an image |
| `docksmith images` | List all images |
| `docksmith inspect <name:tag>` | Show image metadata, layers, and config |
| `docksmith rmi <name:tag>` | Remove an image manifest; delete unreferenced layers only |
| `docksmith prune` | Remove orphan layer files and stale cache entries |
| `docksmith import -t <name:tag> <rootfs.tar>` | Import a base image from a tarball |

### `inspect` example

```bash
$ docksmith inspect myapp:latest

Image: myapp:latest
Digest: sha256:...
Created: 2026-01-01T00:00:00Z

Layers:
  1. sha256:abc...
     SIZE: 12.4 MB
     CREATED BY: FROM alpine:latest
  2. sha256:def...
     SIZE: 1.2 KB
     CREATED BY: COPY hello.sh /app/
  3. sha256:ghi...
     SIZE: 43 KB
     CREATED BY: RUN chmod +x /app/hello.sh

Config:
  WorkingDir: /app
  Env:
    PORT=8080
  Cmd:
    /app/hello.sh
```

## Project Structure

```
docksmith/
├── main.go                  # Entry point + CLI commands
├── builder/                 # Build engine with cache coordination
├── cache/                   # Cache key computation & index management
├── layer/                   # Tar creation, extraction, whiteouts, glob
│   ├── layer.go
│   └── whiteout.go
├── manifest/                # Manifest types & digest computation
├── parser/                  # Docksmithfile parser (6 instructions)
├── runtime/                 # Container runtime (namespaces + chroot)
│   ├── runtime.go           # Linux implementation
│   ├── runtime_other.go     # Non-Linux stubs
│   └── isolation_linux.go   # Capability dropping
├── store/                   # Image store, layer GC
│   ├── store.go
│   └── gc.go
├── integration/             # End-to-end build tests
├── sample-app/              # Demo app using all 6 instructions
└── scripts/
    ├── setup-base-image.sh  # Import Alpine via Docker
    └── benchmark.sh         # Cold/warm build benchmarks
```

## Storage Layout

```
~/.docksmith/
├── images/     # JSON manifests (one per image)
├── layers/     # Content-addressed tar files (named by SHA-256 digest)
└── cache/      # Cache index mapping keys → layer digests
```

Layers are shared across images by digest. Removing one image does not delete layers still referenced by another.

## Testing

Automated tests cover the parser, cache, layers (including whiteouts), store/GC, manifests, and integration builds.

```bash
# All packages (parser, cache, layer, store, integration — runs on any OS)
go test ./...

# Linux runtime isolation test (requires root; skipped otherwise).
# The test re-execs a copy of itself — no separate docksmith binary needed.
sudo go test ./runtime/...
```

Tests use `store.SetHomeForTest()` so nothing is written to `~/.docksmith`.

## Benchmarking

Go benchmarks for cache key computation, layer creation, snapshotting, and extraction:

```bash
go test -bench=. -benchmem ./layer ./cache
```

End-to-end cold vs warm build comparison (Linux/WSL):

```bash
chmod +x scripts/benchmark.sh
./scripts/benchmark.sh
```

The script builds contexts with 10, 100, and 1,000 files, times cold and warm builds, runs Go micro-benchmarks, and optionally compares against Docker when available.

## Demo Sequence

| # | Command | Expected Result |
|---|---------|-----------------|
| 1 | `docksmith build -t myapp:latest ./sample-app` (cold) | All steps show `[CACHE MISS]` |
| 2 | `docksmith build -t myapp:latest ./sample-app` (warm) | All steps show `[CACHE HIT]` |
| 3 | Edit `data.txt`, rebuild | Affected step + below = `[CACHE MISS]`, above = `[CACHE HIT]` |
| 4 | `docksmith images` | Table with Name, Tag, ID, Created |
| 5 | `docksmith inspect myapp:latest` | Digest, layers, env, cmd, workdir |
| 6 | `docksmith run myapp:latest` | Container prints greeting and data |
| 7 | `docksmith run -e GREETING=Goodbye myapp:latest` | ENV override applied |
| 8 | Write file inside container, check host | File must NOT appear on host (isolation test) |
| 9 | `docksmith rmi myapp:latest` | Manifest removed; shared layers kept |
| 10 | `docksmith prune` | Orphan layers and stale cache entries removed |

## Key Design Decisions

- **Re-exec pattern**: the binary re-executes itself with `__child__` as the first argument inside new namespaces, then performs chroot
- **Reproducible tars**: all tar entries sorted lexicographically, timestamps zeroed, uid/gid set to 0
- **Whiteout semantics**: file deletions in `RUN` layers use `.wh.<name>` entries; directory deletions use opaque-directory markers
- **Reference-counted layers**: layer files are deleted only when no manifest references them
- **User namespaces + capability dropping**: child process maps container root to the host user and clears capability sets
- **No external dependencies**: uses only the Go standard library
- **Network namespace** (`CLONE_NEWNET`): blocks all network access during both `RUN` and `docksmith run`
- **Single binary**: one `main.go` plus internal packages

## Isolation

Implemented:

| Feature | Details |
|---------|---------|
| PID namespace | `CLONE_NEWPID` — isolated process tree |
| Mount namespace | `CLONE_NEWNS` — private mount view |
| UTS namespace | `CLONE_NEWUTS` — hostname set to `docksmith` |
| Network namespace | `CLONE_NEWNET` — no network interfaces |
| User namespace | `CLONE_NEWUSER` with uid/gid mappings |
| chroot | Filesystem rooted at assembled image layers |
| Capability dropping | `PR_SET_NO_NEW_PRIVS` + `capset` in child |
| proc mount | `/proc` mounted inside container root |

Not implemented: seccomp, cgroups, overlayfs, volume mounts, container networking.

## Further Reading

See [GUIDE.md](GUIDE.md) for a deeper architecture walkthrough and interview-oriented explanations.
