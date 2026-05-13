# Docksmith

A simplified Docker-like build and runtime system built from scratch in Go.



## What is Docksmith?

Docksmith is a simplified container build and runtime system that implements three core concepts:

1. **Build System** — Reads a `Docksmithfile` (similar to Dockerfile) with 6 instructions: `FROM`, `COPY`, `RUN`, `WORKDIR`, `ENV`, `CMD`. Each `COPY` and `RUN` produces an immutable, content-addressed layer stored as a tar file.

2. **Build Cache** — Deterministic caching with SHA-256 keys computed from the previous layer digest, instruction text, WORKDIR, ENV state, and (for COPY) file content hashes. Cache misses cascade to all subsequent steps.

3. **Container Runtime** — Linux process isolation using `chroot` + namespaces (PID, mount, UTS, network). The same isolation primitive is used for both `RUN` during build and `docksmith run`.

## Prerequisites

- **Linux** (or WSL2 on Windows)
- **Go 1.21+**
- **Root privileges** (needed for chroot/namespace syscalls)
- **Docker** (only for initial base image setup)

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

### 5. Test ENV override

```bash
sudo ./docksmith run -e GREETING=Goodbye myapp:latest
```

## CLI Reference

| Command | Description |
|---------|-------------|
| `docksmith build -t <name:tag> [--no-cache] <context>` | Build an image from a Docksmithfile |
| `docksmith run [-e KEY=VALUE]... <name:tag> [cmd...]` | Run a container from an image |
| `docksmith images` | List all images |
| `docksmith rmi <name:tag>` | Remove an image and its layers |
| `docksmith import -t <name:tag> <rootfs.tar>` | Import a base image from a tarball |

## Project Structure

```
docksmith/
├── main.go              # Entry point + all CLI commands
├── builder/             # Build engine with cache coordination
│   └── builder.go
├── cache/               # Cache key computation & index management
│   └── cache.go
├── layer/               # Reproducible tar creation & extraction
│   └── layer.go
├── manifest/            # Manifest types & digest computation
│   └── manifest.go
├── parser/              # Docksmithfile parser (6 instructions)
│   └── parser.go
├── runtime/             # Container runtime (chroot + namespaces)
│   └── runtime.go
├── store/               # Image store (manifest CRUD, layer files)
│   └── store.go
├── sample-app/          # Sample application using all 6 instructions
│   ├── Docksmithfile
│   ├── hello.sh
│   └── data.txt
└── scripts/
    └── setup-base-image.sh
```

## Storage Layout

```
~/.docksmith/
├── images/     # JSON manifests (one per image)
├── layers/     # Content-addressed tar files (named by SHA-256 digest)
└── cache/      # Cache index mapping keys → layer digests
```

## Demo Sequence

| # | Command | Expected Result |
|---|---------|-----------------|
| 1 | `docksmith build -t myapp:latest ./sample-app` (cold) | All steps show `[CACHE MISS]` |
| 2 | `docksmith build -t myapp:latest ./sample-app` (warm) | All steps show `[CACHE HIT]` |
| 3 | Edit `data.txt`, rebuild | Affected step + below = `[CACHE MISS]`, above = `[CACHE HIT]` |
| 4 | `docksmith images` | Table with Name, Tag, ID, Created |
| 5 | `docksmith run myapp:latest` | Container prints greeting and data |
| 6 | `docksmith run -e GREETING=Goodbye myapp:latest` | ENV override applied |
| 7 | Write file inside container, check host | File must NOT appear on host (isolation test) |
| 8 | `docksmith rmi myapp:latest` | Manifest + layers removed from `~/.docksmith/` |

## Key Design Decisions

- **Re-exec pattern**: the binary re-executes itself with `__child__` as the first argument inside new namespaces, then performs chroot
- **Reproducible tars**: all tar entries sorted lexicographically, timestamps zeroed, uid/gid set to 0
- **No external dependencies**: uses only Go standard library
- **Network namespace** (`CLONE_NEWNET`): blocks all network access during both `RUN` and `docksmith run`
- **Single binary**: everything in one `main.go` + 6 internal packages
