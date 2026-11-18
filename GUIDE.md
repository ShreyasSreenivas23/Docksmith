# Docksmith — Interview Preparation Guide

> A simplified Docker-like build & runtime system built from scratch in Go
> 1,759 LOC across 7 packages |

---

## 1. Project Overview (30-Second Pitch)

**Docksmith** is a from-scratch container build and runtime system that implements three core capabilities:

1. **Build Engine** — Reads a `Docksmithfile` (Dockerfile-like) with 6 instructions (FROM, COPY, RUN, WORKDIR, ENV, CMD) and produces layered, content-addressed images.
2. **Build Cache** — Deterministic SHA-256 caching that skips re-execution of unchanged steps (cache key = previous layer digest + instruction text + workdir + env state + file hashes).
3. **Container Runtime** — Linux process isolation via `chroot` + 4 namespaces (PID, mount, UTS, network) to securely execute commands and run containers.

Built entirely with Go's standard library — zero external dependencies.

---

## 2. Architecture & Design

### 2.1 High-Level Architecture

```
┌─────────────────────────────┐
│  CLI Layer (main.go)        │  ← Command parsing, help, dispatch
├─────────────────────────────┤
│  Build (builder + cache     │  ← Instruction processing, caching
│         + manifest)         │
├─────────────────────────────┤
│  Runtime (parser + runtime) │  ← Namespace isolation, execution
├─────────────────────────────┤
│  Layers (layer)             │  ← Tar creation, glob, extraction
├─────────────────────────────┤
│  Storage (store)            │  ← Persistence, serialization
└─────────────────────────────┘
```

### 2.2 Storage Layout

```
~/.docksmith/
├── images/     ← JSON manifests (one per image)
├── layers/     ← Content-addressed tar files (SHA-256 name)
└── cache/      ← Cache index mapping keys → layer digests
```

### 2.3 Data Flow — `docksmith build`

```
Docksmithfile
    │
    ▼
parser.Parse()          ← Converts file to []Instruction
    │
    ▼
builder.Build()         ← Iterates instructions
    │
    ├── FROM  → Load base manifest, inherit layers/env/cmd/workdir
    ├── COPY  → Compute cache key → Lookup → [HIT|MISS]
    │           MISS: layer.CreateCopyLayer() → Store tar → cache.Store()
    ├── RUN   → Extract layers → SnapshotDir → RunIsolated()
    │           → CreateRunLayer(delta) → Store tar → cache.Store()
    ├── ENV   → Update envState map
    ├── WORKDIR → Update workdir variable
    └── CMD   → Store cmdArgs
         │
         ▼
    store.SaveManifest()  ← ComputeDigest + Serialize + Write JSON
```

### 2.4 Data Flow — `docksmith run`

```
store.LoadManifest(name, tag)   ← Read image manifest JSON
    │
    ▼
runtime.AssembleFilesystem()    ← Extract all layers in order to temp dir
    │
    ▼
runtime.RunIsolated()           ← Re-exec self in new namespaces
    │
    ▼
ChildProcess()                  ← Mount proc → chroot → chdir → exec
```

---

## 3. Key Design Decisions & Trade-offs

### 3.1 Re-exec Pattern for Namespace Isolation

**Decision:** The binary re-executes itself with `__child__` as the first argument inside new namespaces, rather than using a separate init process or daemon.

**Why:** Namespace creation via `Cloneflags` must happen *before* the process starts. The re-exec pattern allows the parent to create namespaces, and the child (after `chroot`) to execute the actual command. This is the same pattern Docker used in its early days.

**Code path:** `main.go:31-37` → `runtime/runtime.go:44-50` (Cloneflags) → `runtime/runtime.go:63-112` (ChildProcess)

### 3.2 Content-Addressed Layers

**Decision:** Each layer is stored as a tar file named by its SHA-256 digest (`sha256:<hex>`). Layers are immutable — once written, `StoreTar()` returns immediately if the file already exists.

**Why:** Content addressing provides:
- **Deduplication**: Same layer content = same hash = one file on disk
- **Integrity**: The name IS the checksum — corruption is detectable
- **Determinism**: Builds are reproducible when inputs are identical

**Code path:** `layer/layer.go:524-527` (computeDigest), `layer/layer.go:250-258` (StoreTar with immutability check)

### 3.3 Deterministic Build Cache

**Decision:** Cache key = SHA-256 of `(prevDigest + instructionText + workdir + envState + [fileHashes for COPY])`.

**Why:** This captures every input that could affect the output:
- `prevDigest`: Ensures the entire layer chain is considered
- `instructionText`: The exact RUN command or COPY source/dest
- `workdir`: Affects where RUN executes
- `envState`: Environment variables can change command behavior
- `fileHashes`: For COPY, the actual file contents that will be copied

**Why NOT just mtime:** Modification timestamps are not reproducible across machines. Content hashing ensures the cache is valid regardless of when files were modified.

**Code path:** `cache/cache.go:92-101`

### 3.4 Cascade Cache Miss

**Decision:** When one step misses the cache (`cascadeMiss = true`), all subsequent steps are forced to miss too.

**Why:** If a previous layer changed, the next layer's `prevDigest` in the cache key would be different anyway. This optimization avoids computing cache keys for steps that would definitely miss.

**Code path:** `builder/builder.go:42` (initialization), `builder/builder.go:126` (set on miss)

### 3.5 Reproducible Tar Archives

**Decision:** All tar entries are:
- Sorted lexicographically
- Timestamps zeroed (`time.Time{}`)
- UID/GID set to 0
- Paths normalized to forward slashes

**Why:** Standard `tar` archives include metadata (timestamps, owners) that differ across machines. By zeroing these and sorting entries, the same content always produces identical tar bytes → identical digest → cache hits across machines.

**Code path:** `layer/layer.go:82-89` (COPY layer headers), `layer/layer.go:169-176` (RUN layer headers), `main.go:359-427` (normalizeTar for imports)

### 3.6 Network Namespace Isolation

**Decision:** `CLONE_NEWNET` is used to block all network access during both `RUN` and `docksmith run`.

**Why:** This is a security measure — commands executed during build should not access the network. It also simplifies the implementation (no need for network bridge, iptables, etc.).

**Code path:** `runtime/runtime.go:48`

### 3.7 Single Binary, Zero Dependencies

**Decision:** The entire system is a single Go binary with zero external dependencies — only the Go standard library.

**Why:** Go compiles to a static binary. No libcontainer, no runc, no containerd. This was an academic constraint and also demonstrates deep understanding of the underlying OS primitives.

**Code path:** `go.mod:1-3` (no require directives)

---

## 4. Package-by-Package Deep Dive

### 4.1 `parser/parser.go` (175 lines)

**Purpose:** Parse a `Docksmithfile` into a structured instruction list.

**Key structures:**
- `InstructionType` enum: `FROM | COPY | RUN | WORKDIR | ENV | CMD`
- `Instruction` struct: Type + contextual fields (FromImage, CopySrc, CmdArgs, etc.)

**Key functions:**
- `Parse(contextDir)` → reads `Docksmithfile` from directory, calls `ParseContent()`
- `ParseContent(content)` → splits lines, filters comments/blanks, validates first instruction is FROM
- `parseLine(line, lineNum)` → switches on keyword, extracts args:
  - `FROM`: split on `:`, default tag "latest"
  - `COPY`: exactly 2 args (src, dest)
  - `RUN`: capture shell command string
  - `ENV`: find `=` index, split key/value
  - `CMD`: JSON `Unmarshal` into `[]string`
  - `WORKDIR`: capture path

**Edge cases handled:**
- Empty lines and `#` comments are skipped
- First instruction MUST be FROM (error otherwise)
- CMD validates it's a non-empty JSON array
- ENV requires `KEY=VALUE` format with non-empty key

### 4.2 `builder/builder.go` (325 lines)

**Purpose:** Orchestrate the entire build pipeline — iterate instructions, manage state, coordinate caching and layer creation.

**Key state tracked during build:**
- `layers []manifest.Layer`: Accumulated layer stack
- `workdir string`: Current working directory
- `envState map[string]string`: Environment variable state
- `cmdArgs []string`: Default command
- `prevDigest string`: Digest of the last layer (for cache chaining)
- `cascadeMiss bool`: Force cache miss after first miss
- `allCacheHit bool`: Track if entire build was cached

**Instruction handlers:**
| Instruction | What happens |
|---|---|
| `FROM` | Load base manifest, inherit its layers/env/cmd/workdir, set prevDigest |
| `WORKDIR` | Update workdir variable |
| `ENV` | Merge into envState map |
| `CMD` | Store command args |
| `COPY` | Compute cache key → Lookup → if MISS: CreateCopyLayer → StoreTar → Store cache |
| `RUN` | Create temp dir → ExtractLayers → SnapshotDir → RunIsolated → CreateRunLayer → StoreTar → Store cache → Cleanup |

**Manifest creation (end of build):**
- Build sorted env list from envState
- Preserve original `Created` timestamp if all steps hit cache (idempotent rebuilds)
- Call `store.SaveManifest(m)` which computes digest and writes JSON

### 4.3 `cache/cache.go` (148 lines)

**Purpose:** Deterministic build caching — compute keys, persist index, look up/store entries.

**Key structures:**
- `Index` struct: `map[string]string` (cache key → layer digest), persisted as JSON

**Key functions:**
- `Lookup(key)`: Load index, check key exists AND referenced layer is on disk
- `Store(key, digest)`: Load index, update entry, save
- `ComputeCacheKey(prevDigest, instructionText, workdir, envState, fileHashes)`: SHA-256 of concatenated deterministic serializations
- `serializeEnvState()`: Sort keys, join as `KEY=VALUE\n`
- `serializeFileHashes()`: Sort paths, concatenate hex digests

**Caching invariants:**
- Thread-safe via `sync.Mutex`
- Layer existence validated on lookup (stale cache entries are ignored)
- `Index` is file-based JSON (simple, no database needed)

### 4.4 `layer/layer.go` (561 lines)

**Purpose:** All filesystem operations — tar creation, glob matching, snapshot/delta, extraction.

**Key functions:**

| Function | Purpose |
|---|---|
| `CreateCopyLayer()` | Build tar from context dir source files |
| `CreateRunLayer()` | Delta tar: compare before/after snapshots |
| `SnapshotDir()` | Hash all files in a directory tree |
| `StoreTar()` | Immutable write-once layer storage |
| `ExtractLayers()` | Extract layers in order into target |
| `extractTar()` | Read tar, create files/dirs/symlinks/hardlinks with path traversal prevention |
| `resolveSourceFiles()` | Resolve COPY source (glob or literal) |
| `globUnderDir()` | Recursive `**` glob pattern matching |
| `matchSegments()` | Recursive segment-by-segment glob engine |
| `GetSourceFileHashes()` | File hashes for COPY cache key |
| `hashFileContents()` | SHA-256 of a single file |

**Glob engine details:**
- `*`, `?`, `[]` delegated to `filepath.Match()`
- `**` handled by custom `matchSegments()` recursion:
  - Try matching zero segments first
  - Then try consuming 1, 2, ... up to all remaining segments
  - First match wins (boolean match, not file enumeration)

**Delta detection algorithm:**
1. `SnapshotDir()` → walk filesystem, record `path → hash` (dirs get marker)
2. `CreateRunLayer()` → walk filesystem again, compare:
   - New directory? → included
   - New file? → included
   - Existing file, hash changed? → included
   - Existing file, hash same? → skipped
   - Existing file, hash missing from snapshot? → included
   - proc/, dev/, sys/ → excluded

**Security in tar extraction:**
- `filepath.Clean(hdr.Name)` normalizes paths
- `strings.HasPrefix(cleanName, "..")` → skip (path traversal prevention)

### 4.5 `manifest/manifest.go` (72 lines)

**Purpose:** Data structures and serialization for image manifests.

**Key types:**
```go
type Layer struct {
    Digest    string // "sha256:..."
    Size      int64  // bytes
    CreatedBy string // e.g. "COPY hello.sh /app/hello.sh"
}

type Config struct {
    Env        []string // ["PATH=/usr/bin", ...]
    Cmd        []string // ["/bin/sh", "/app/hello.sh"]
    WorkingDir string   // "/app"
}

type Manifest struct {
    Name    string
    Tag     string
    Digest  string  // SHA-256 of canonical JSON (with digest="")
    Created string  // RFC3339
    Config  Config
    Layers  []Layer
}
```

**Digest computation:**
1. Set `Digest = ""`
2. `json.Marshal(m)` (canonical JSON)
3. SHA-256 the result → `"sha256:hex"`
4. This means the digest depends on ALL fields (name, tag, config, layers)

**`ShortID()`:** Returns hex characters [7:19] of the digest string (12 chars), similar to Docker's short image IDs.

### 4.6 `runtime/runtime.go` (185 lines) + `runtime_other.go`

**Purpose:** Container execution — create namespaces, chroot, execute commands.

**Linux-only** (build tag: `//go:build linux`). Non-Linux builds use `runtime_other.go` which provides stubs.

**`RunIsolated()` — The isolation primitive (used by both build RUN and `docksmith run`):**
1. Create `proc/`, `dev/`, workdir inside rootDir
2. Re-exec self with `__child__` argument:
   - `useShell=true` → `/proc/self/exe __child__ <rootDir> <workdir> shell <cmd>`
   - `useShell=false` → `/proc/self/exe __child__ <rootDir> <workdir> exec <cmd0> <args...>`
3. Set `Cloneflags`:
   - `CLONE_NEWPID` — Isolated PID namespace (processes inside can't see host processes)
   - `CLONE_NEWNS` — Isolated mount namespace (mounts don't leak to host)
   - `CLONE_NEWUTS` — Isolated hostname (can set hostname independently)
   - `CLONE_NEWNET` — Isolated network (no network access)
4. `Unshareflags: CLONE_NEWNS` — Unshare mount namespace for the child

**`ChildProcess()` — Runs inside the new namespaces:**
1. Mount `proc` filesystem (required for `/bin/sh` and many commands)
2. `syscall.Chroot(rootDir)` — Change root to layer filesystem
3. `syscall.Chdir(workdir)` — Set working directory
4. `syscall.Sethostname("docksmith")` — Set hostname
5. Execute command:
   - Shell mode: `/bin/sh -c "<command>"`
   - Exec mode: direct exec of command with args
6. Inherit stdin/stdout/stderr, propagate exit code

**`AssembleFilesystem()`:**
1. Create temp directory
2. Extract all layers in order
3. Return path (caller responsible for cleanup)

**`Run()` — `docksmith run` entry point:**
1. Load manifest by `name:tag`
2. Determine command: override > image CMD
3. Merge env: image ENV + overrides
4. Assemble filesystem
5. Call `RunIsolated()` in exec mode

### 4.7 `store/store.go` (139 lines)

**Purpose:** Persistent storage — directory management, manifest CRUD, layer paths.

**Directory layout:**
```
~/.docksmith/
├── images/     ← {name}_{tag}.json
├── layers/     ← sha256_{hex}.tar
└── cache/      ← index.json
```

**Key functions:**
- `SaveManifest(m)`: ComputeDigest → Serialize → Write JSON
- `LoadManifest(name, tag)`: Read JSON → Deserialize
- `ListManifests()`: Enumerate `*.json` files in images dir → Deserialize each
- `RemoveImage(name, tag)`: Load manifest → Delete all layer files → Delete manifest
- `LayerPath(digest)`: `~/.docksmith/layers/sha256_{hex}.tar`
- `LayerExists(digest)`: Check file exists

### 4.8 `main.go` (427 lines)

**Purpose:** Entry point, CLI command parsing, tar normalization utilities.

**Commands:**
| Command | Description |
|---|---|
| `docksmith build -t name:tag [--no-cache] <context>` | Build image |
| `docksmith run [-e KEY=VALUE]... <name:tag> [cmd...]` | Run container |
| `docksmith images` | List images |
| `docksmith rmi <name:tag>` | Remove image |
| `docksmith import -t name:tag <rootfs.tar>` | Import base image |
| `docksmith help` | Show usage |

**Re-exec dispatch (`main.go:31-37`):**
When invoked with `__child__`, delegates to `runtime.ChildProcess()`

**Utility functions:**
- `parseNameTag(nameTag)`: Split on last `:`, default tag "latest"
- `normalizeTar(srcData)`: Read tar → clean headers (UID=0, GID=0, zeroTime) → sort entries → rewrite tar. Used during import for reproducible base images.

---

## 5. Interview Q&A — Everything You Could Be Asked

### 5.1 Architecture & Design

**Q: How is Docksmith different from Docker?**
A: Docker is a production-grade container system with image registries, container networking, volume management, orchestration (Docker Compose/Swarm), and millions of lines of code. Docksmith is a simplified implementation (~1,700 LOC) that demonstrates core container concepts: layered images, content-addressed storage, build caching, and namespace-based isolation. It runs on Linux only, requires root, and has no networking, volume mounts, or registry support.

**Q: Why did you build this from scratch rather than using Docker's libraries?**
A: The goal was to demonstrate understanding of the underlying OS primitives and container concepts. By implementing everything with Go's standard library, we show proficiency with Linux namespaces (`CLONE_NEWPID`, `CLONE_NEWNS`, `CLONE_NEWUTS`, `CLONE_NEWNET`), chroot, content-addressed storage, and build caching. Using a library would have hidden these details.

**Q: Explain the layered image model.**
A: Each instruction in the Docksmithfile produces either a COPY layer (tar of source files) or a RUN layer (delta tar of files changed by the command). Layers are stacked — when running a container, all layers are extracted in order, with later layers overwriting earlier ones. This is the same model Docker uses. Benefits: deduplication (shared base layers), cache efficiency (only rebuild changed layers), and smaller storage (incremental deltas).

**Q: How does the build cache work?**
A: The cache is a JSON index file mapping SHA-256 keys to layer digests. Each key captures all inputs that affect the output: previous layer digest, exact instruction text, working directory, environment state, and (for COPY) content hashes of source files. On cache hit, the layer is reused from storage without re-execution. On miss, the command runs, the new layer is stored, and the cache is updated. A "cascade miss" flag skips lookups for all subsequent steps when one step misses, since they would also miss (their prevDigest changed).

**Q: How do you guarantee build reproducibility?**
A: Three mechanisms: (1) Content-addressed layers — digests are SHA-256 of tar bytes, not timestamps. (2) Reproducible tars — zero timestamps, zero UID/GID, sorted entries, so same content always produces identical tar bytes. (3) Deterministic cache keys — all inputs (env, file content, instruction text) are deterministically serialized and hashed.

### 5.2 Linux Namespaces & Isolation

**Q: What namespaces do you use and why?**
A: We use four:
- **PID** (`CLONE_NEWPID`): Container processes see only their own process tree, not host processes. PID 1 inside the container is the first process.
- **Mount** (`CLONE_NEWNS`): Mount operations inside the container (like `/proc`) don't affect the host. Combined with `chroot`, this provides filesystem isolation.
- **UTS** (`CLONE_NEWUTS`): The container can have its own hostname (`docksmith`), independent of the host.
- **Network** (`CLONE_NEWNET`): The container gets its own network stack with no interfaces — effectively blocking all network access for security.

**Q: Why not use user namespaces?**
A: User namespaces (`CLONE_NEWUSER`) would allow running without root by mapping container UID 0 to a non-root host UID. We didn't implement this because: (1) It adds complexity (UID/GID mapping configuration, capabilities). (2) Our academic scope prioritized demonstrating core concepts. (3) Cross-platform support is limited (requires kernel support + configuration).

**Q: Explain the re-exec pattern.**
A: Linux namespace flags (`Cloneflags`) must be set at process creation via `clone()` or `exec()` — they can't be applied to an already-running process. So we use a two-process model: the parent (builder or run command) sets up the environment (creates temp dir, extracts layers) and then re-executes itself via `exec.Command("/proc/self/exe", "__child__", ...)`. The new child process inherits new namespaces, performs chroot, mounts proc, and executes the actual command. This is the same pattern Docker's initial implementations used before libcontainer.

**Q: How do you handle mount propagation?**
A: We use `Unshareflags: syscall.CLONE_NEWNS` when creating the child process. This ensures mounts we perform inside the container (like mounting `/proc`) don't propagate to the host mount namespace. Without this, mounting proc inside the container could leak to the host.

### 5.3 Storage & Content Addressing

**Q: Explain content-addressed storage.**
A: Each layer is stored as a tar file whose filename IS its SHA-256 digest. This means: (1) Identical content always produces the same filename — deduplication for free. (2) The name is a cryptographic integrity check — if the file is corrupted, the hash won't match. (3) Layers are immutable — once written, we never overwrite (`StoreTar` returns early if the file exists). Storage path: `~/.docksmith/layers/sha256_<hex>.tar`.

**Q: How does the manifest work?**
A: The manifest is a JSON file that describes the image: its name/tag, configuration (env, cmd, workdir), creation time, and the ordered list of layers (each with digest + size + createdBy). The manifest itself has a SHA-256 digest computed from the canonical JSON (with its own digest field blanked). This means any change to the image config or layer list produces a different image digest. Stored at `~/.docksmith/images/{name}_{tag}.json`.

**Q: What's the "ShortID"?**
A: Similar to Docker's short image ID — it's the first 12 hex characters of the SHA-256 digest (the characters at positions 7-18 of the `sha256:...` string). This gives 48 bits of collision resistance, sufficient for local identification.

### 5.4 Build System

**Q: Walk through what happens for a COPY instruction.**
A: When the builder encounters COPY:
1. It calls `layer.GetSourceFileHashes()` to hash source files (for cache key).
2. It computes a cache key from: `prevDigest + "COPY hello.sh /app/hello.sh" + workdir + envState + fileHashes`.
3. If the cache has this key AND the referenced layer exists on disk → **CACHE HIT**: layer reused.
4. If **CACHE MISS**: `CreateCopyLayer()` builds a tar from source files, `StoreTar()` persists it, `cache.Store()` updates the index.
5. The layer is appended to the manifest list, `prevDigest` is updated.

**Q: Walk through what happens for a RUN instruction.**
A: When the builder encounters RUN:
1. Compute cache key from `prevDigest + instructionText + workdir + envState` (no file hashes — we're executing a command, not copying files).
2. Cache lookup as above.
3. On miss: create temp dir, extract ALL previous layers into it, take a filesystem snapshot (hash every file), execute the command via `RunIsolated()` in shell mode, take another snapshot, compare to find changed/new files, create a delta tar of just those changes, store the tar, update the cache.
4. Clean up temp directory.

**Q: What happens if a previous layer changes?**
A: The `cascadeMiss` flag ensures all subsequent steps re-execute. But more importantly, the cache key includes `prevDigest`, so even without the cascade flag, each step's cache key would be different because `prevDigest` changed. The cascade is just an optimization to skip computing cache keys that would definitely miss.

### 5.6 Edge Cases & Failure Modes

**Q: What happens if a COPY source doesn't exist?**
A: `resolveSourceFiles()` returns an error: either `"source not found"` for a literal path or glob could return zero matches leading to `"no files matched"`. The builder propagates this as a fatal build error. We don't silently skip missing files — that would produce inconsistent images.

**Q: What if a RUN command exits with non-zero?**
A: The builder checks `exitCode != 0` and returns an error: `"RUN command exited with code <N>"`. The temp directory is cleaned up before returning. This prevents building images from failed steps.

**Q: What safeguards exist against path traversal in tar extraction?**
A: The `extractTar()` function calls `filepath.Clean(hdr.Name)` and then checks `strings.HasPrefix(cleanName, "..")`. If the cleaned name starts with `..`, the entry is silently skipped. This prevents a malicious tar from writing files outside the target directory.

**Q: What happens if you run `docksmith run` on an image with no CMD?**
A: The `Run()` function checks `len(cmd) == 0` and returns an error: `"no CMD defined in image X and no command given"`. However, if the user provides a command after the image reference (e.g., `docksmith run myapp:latest /bin/sh`), that override is used instead.

### 5.7 Performance & Scalability

**Q: How would you scale this for production?**
A: Several improvements would be needed: (1) Concurrent layer extraction for faster filesystem assembly. (2) A more sophisticated cache eviction policy (ours keeps all entries forever). (3) Remote registries for image distribution (Docker Hub-like). (4) Lazy layer pulling (download only when needed, not all layers upfront). (5) Union filesystem (overlayfs, aufs) instead of extracting all layers to disk.

**Q: What's the performance bottleneck in `docksmith run`?**
A: Currently, every layer tar is fully extracted to a temp directory when starting a container. For images with many layers or large layers, this means significant disk I/O and space. Docker solves this with overlayfs — layers are stacked without copying. Our approach is simpler but substantially slower for large images.

**Q: How does cache storage grow over time?**
A: The cache index (`index.json`) grows linearly with the number of unique cache keys. Layer tar files accumulate in `~/.docksmith/layers/` but are deduplicated by content. There's no eviction policy — stale entries (referencing layers that were deleted via `rmi`) are silently filtered by `Lookup()` which checks layer existence. A production system would need cache eviction (LRU, TTL, or disk usage limits).

### 5.8 Comparison & Context

**Q: How does this compare to Docker's buildkit?**
A: BuildKit is Docker's modern build system with concurrent build graphs, LLB (Low-Level Build) intermediate representation, distributed caching, lazy layer mounting with snapshotters, and multi-platform builds. Docksmith is a simplified sequential executor — one instruction at a time, no build graph, no concurrency. The cache model is similar in concept (content-hash keys) but far less sophisticated.

**Q: What would you add if you had more time?**
A: (1) Overlayfs support to avoid full layer extraction. (2) Multi-stage builds (multiple FROM instructions). (3) `.docksmithignore` file (like `.dockerignore`). (4) User namespace support to run without root. (5) Remote registry push/pull. (6) Concurrent layer downloads. (7) Health checks and container lifecycle management.

**Q: Why Go rather than Rust or C?**
A: Go was chosen for: (1) Built-in concurrency (goroutines) — useful for future parallel layer operations. (2) Cross-compilation — single binary output. (3) Excellent standard library — `archive/tar`, `crypto/sha256`, `os/exec`, `syscall` provided everything needed without external dependencies. (4) Fast compilation — important for rapid iteration in an academic setting. (5) Garbage collection — reduced memory management complexity compared to C/Rust.

### 5.9 Code-Specific Questions

**Q: Why is `normalizeTar` in `main.go` rather than in `layer.go`?**
A: This was an artifact of our team splitting strategy. `normalizeTar` is only used by the `import` command (in `main.go`), not by the layer package itself, so it was placed with the CLI utilities. Architecturally, it could live in `layer/` or as its own utility.

**Q: Why does `Index` have a `sync.Mutex` but `Lookup` acquires it?**
A: The `cache.go` file has a package-level `sync.Mutex` (`var mu sync.Mutex`) to protect the JSON index file from concurrent read/write. Both `Lookup` (read) and `Store` (write) acquire the mutex to prevent race conditions. In a single-threaded CLI this is unnecessary, but it's defensive programming for future concurrent use.

**Q: Why does `SnapshotDir` skip `proc/`, `dev/`, `sys/`?**
A: These are virtual filesystems mounted by the runtime (`proc` is mounted via `syscall.Mount`). Their contents are dynamic and not part of the image. Comparing their contents before and after a RUN would produce false positives (changed proc entries) and could include files (like `/dev/zero`) that shouldn't be in an image.

**Q: How does `ComputeDigest` work and why set digest to empty first?**
A: `ComputeDigest` sets `m.Digest = ""`, then marshals to JSON and SHA-256 hashes the result. This is a chicken-and-egg problem — the manifest's digest depends on its contents, but the digest field is part of those contents. By blanking it before hashing, we compute a digest of everything EXCEPT the digest itself. After computation, the callers (like `SaveManifest`) will write the full JSON including the digest. This is the same approach Docker uses.

### 5.10 Teamwork & Process

**Q: How was the work split among 4 team members?**
A: We divided by functional module with clear boundaries to avoid merge conflicts:
- **Person 1 (Alpha)**: Build engine + Cache + Manifest data structures (434 LOC, 3 files)
- **Person 2 (Beta)**: Layer operations, tar creation, glob engine (508 LOC, 1 file)
- **Person 3 (Sigma)**: CLI commands + Storage layer (415 LOC, 2 files)
- **Person 4 (Delta)**: Parser + Container runtime + Utilities (402 LOC, 3 files)
Each person had 6-7 commits over one week, following a dependency-aware schedule:
1. Parser first (Person 4) — everything else depends on parsed instructions
2. Build system next (Person 1) — depends on parser
3. Layer operations in parallel (Person 2) — independent of build logic
4. CLI + Storage last (Person 3) — depends on all other components

**Q: What was the biggest integration challenge?**
A: The interfaces between packages had to be agreed upon before implementation. For example, `builder.Build()` calls `layer.CreateCopyLayer()`, `layer.SnapshotDir()`, `runtime.RunIsolated()`, etc. We defined these function signatures early and worked to them. The re-exec pattern was also tricky — the main function has to handle both normal CLI commands and the `__child__` dispatch, which meant Person 3 (CLI) and Person 4 (runtime) had to coordinate closely.

---

## 6. Code Walkthrough — Key Flow

### 6.1 Complete `docksmith build` Flow

```go
// 1. CLI: main.go:97-158
runBuild() → parser.Parse(absContext) → builder.Build(instructions, opts)

// 2. Builder: builder.go:28-325
for each instruction:
  FROM  → store.LoadManifest() → inherit layers/env/cmd/workdir
  COPY  → cache.ComputeCacheKey() → cache.Lookup()
          → [MISS] layer.CreateCopyLayer() → layer.StoreTar() → cache.Store()
          → append to layers, update prevDigest
  RUN   → cache.ComputeCacheKey() → cache.Lookup()
          → [MISS] create tmp dir → layer.ExtractLayers()
          → layer.SnapshotDir() → runtime.RunIsolated() in shell mode
          → layer.CreateRunLayer() → layer.StoreTar() → cache.Store()
          → cleanup tmp dir → append to layers, update prevDigest
  ENV   → update envState map
  WORKDIR → update workdir var
  CMD   → store cmdArgs
→ build sorted env list
→ manifest.Manifest{...} → store.SaveManifest()

// 3. Cache: cache.go:92-101
ComputeCacheKey:
  SHA256(prevDigest + instructionText + workdir + serializeEnvState + serializeFileHashes)

// 4. Layer — COPY: layer.go:21-105
CreateCopyLayer:
  resolveSourceFiles(contextDir, src) → sort files
  for each file: create tar header (zeroTime, uid=0, gid=0) → write file data
  close tar → computeDigest(tarBytes)

// 5. Layer — RUN: layer.go:108-212
CreateRunLayer:
  walk filesystem → collect afterFiles (exclude proc/dev/sys)
  compare with beforeSnapshot → collect changedPaths
  for each changedPath: create tar header → write file data (for dirs: just header)
  close tar → computeDigest(tarBytes)

// 6. Runtime: runtime.go:21-59
RunIsolated:
  Create proc/, dev/, workdir in rootDir
  exec.Command("/proc/self/exe", "__child__", rootDir, workdir, "shell", cmd)
  Cloneflags: NEWPID | NEWNS | NEWUTS | NEWNET
  Unshareflags: NEWNS

// 7. Child Process: runtime.go:63-112
ChildProcess:
  mount proc → chroot → chdir → sethostname
  exec.Command("/bin/sh", "-c", cmd) — with stdin/stdout/stderr
```

### 6.2 Complete `docksmith run` Flow

```go
// 1. CLI: main.go:162-203
runRun() → runtime.Run(name, tag, envOverrides, cmdOverride)

// 2. Runtime: runtime.go:135-185
Run:
  store.LoadManifest(name, tag)
  cmd = cmdOverride or manifest.Config.Cmd
  merge env: image ENV + overrides
  AssembleFilesystem(m) → ExtractLayers into tmp dir
  RunIsolated(tmpDir, cmd, workdir, envVars, useShell=false)

// 3. Isolation (same as build RUN):
RunIsolated:
  exec "/proc/self/exe __child__ <rootDir> <workdir> exec <cmd0> <args...>"
  with Cloneflags
```

---

## 7. Sample Application

The sample app (`sample-app/`) demonstrates all 6 instructions:

```dockerfile
FROM alpine:3.18          # Base image import
WORKDIR /app              # Set working directory
COPY hello.sh /app/hello.sh  # Copy script
COPY data.txt /app/data.txt   # Copy data file
ENV GREETING=Hello        # Set environment variable
RUN chmod +x /app/hello.sh    # Make script executable
CMD ["/bin/sh", "/app/hello.sh"]  # Default command
```

When run, `hello.sh` prints the greeting, hostname, working directory, data file contents, and demonstrates that `ENV` variables are available inside the container. Override with `-e GREETING=Goodbye` to test environment injection.

---

## 8. Key Vulnerabilities / Limitations (Be Honest)

1. **Requires root** — No user namespace support. Must run with `sudo`.
2. **Linux only** — Namespaces and chroot are Linux-specific. No Windows/macOS support.
3. **No union filesystem** — Extracts all layers to disk (slow, space-inefficient).
4. **No network** — `CLONE_NEWNET` with no interfaces means containers can't access the network.
5. **No concurrent builds** — Sequential instruction execution, single build at a time.
6. **No registry** — Images are local only, no push/pull to remote.
7. **No cache eviction** — The cache index grows indefinitely.
8. **No health checks** — No container monitoring, restart policies, or lifecycle management.
9. **No seccomp/AppArmor** — No additional security profiles beyond namespaces.
10. **No resource limits** — No cgroup configuration (CPU, memory limits).

---

## 9. Glossary

| Term | Definition |
|---|---|
| Content-addressed storage | Storage indexed by hash of content (not location) |
| Layer | Immutable tar archive representing a set of filesystem changes |
| Manifest | JSON document describing an image's config, layers, and metadata |
| Digest | SHA-256 hash identifying a layer or manifest (`sha256:hex`) |
| Namespace | Linux kernel feature isolating process resources (PID, mount, etc.) |
| Chroot | Change root directory — restricts file system visibility |
| Re-exec | Pattern where a binary re-executes itself to enter new namespaces |
| Delta layer | Tar containing only files that changed during a RUN instruction |
| Cache cascade | When one step misses cache, all subsequent steps are forced miss |
| Glob | Pattern matching for file paths (`*`, `**`, `?`, `[]`) |
| ZeroTime | Zero-value Go `time.Time{}` — used for reproducible tar timestamps |

---

## 10. Quick Reference — Key File Locations

| What | File | Line |
|---|---|---|
| Entry point + CLI | `main.go:22` | 22 |
| Build orchestration | `builder/builder.go:28` | 28 |
| Cache key computation | `cache/cache.go:92` | 92 |
| COPY layer creation | `layer/layer.go:21` | 21 |
| RUN delta layer | `layer/layer.go:108` | 108 |
| Glob engine | `layer/layer.go:437` | 437 |
| Secure extraction | `layer/layer.go:272` | 272 |
| Namespace isolation | `runtime/runtime.go:21` | 21 |
| Chroot + exec | `runtime/runtime.go:63` | 63 |
| Manifest save/load | `store/store.go:57` | 57 |
| Manifest structs | `manifest/manifest.go:25` | 25 |
| Docksmithfile parser | `parser/parser.go:59` | 59 |
| Tar normalization | `main.go:359` | 359 |
