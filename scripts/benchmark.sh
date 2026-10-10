#!/usr/bin/env bash
# Benchmark docksmith build performance (cold vs warm) and optional Docker comparison.
# Run on Linux/WSL with: ./scripts/benchmark.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${ROOT}/docksmith"
WORKDIR="${TMPDIR:-/tmp}/docksmith-bench-$$"
FILE_COUNTS=(10 100 1000)

build_binary() {
  (cd "$ROOT" && go build -o "$BIN" .)
}

make_context() {
  local count="$1"
  local ctx="${WORKDIR}/ctx-${count}"
  rm -rf "$ctx"
  mkdir -p "$ctx"
  cat >"${ctx}/Docksmithfile" <<EOF
FROM bench-base:latest
COPY files /data/
CMD ["/bin/sh"]
EOF
  mkdir -p "${ctx}/files"
  for i in $(seq 1 "$count"); do
    echo "payload-${i}" >"${ctx}/files/file-${i}.txt"
  done

  # Import minimal base once per benchmark run.
  local base="${WORKDIR}/base.tar"
  mkdir -p "${WORKDIR}/base-root/bin"
  echo '#!/bin/sh' >"${WORKDIR}/base-root/bin/sh"
  chmod +x "${WORKDIR}/base-root/bin/sh"
  tar -cf "$base" -C "${WORKDIR}/base-root" .
  "$BIN" import -t bench-base:latest "$base"
  return 0
}

time_build() {
  local ctx="$1"
  local label="$2"
  local start end
  start=$(date +%s.%N)
  "$BIN" build -t "bench:${label}" "$ctx" >"${WORKDIR}/build-${label}.log"
  end=$(date +%s.%N)
  awk -v s="$start" -v e="$end" 'BEGIN { printf "%.3f", e - s }'
}

mkdir -p "$WORKDIR"
build_binary

printf "%-12s %-14s %-14s\n" "Files" "Cold (s)" "Warm (s)"
printf "%-12s %-14s %-14s\n" "----" "--------" "--------"

for n in "${FILE_COUNTS[@]}"; do
  make_context "$n"
  ctx="${WORKDIR}/ctx-${n}"
  cold=$(time_build "$ctx" "cold-${n}")
  warm=$(time_build "$ctx" "warm-${n}")
  printf "%-12d %-14s %-14s\n" "$n" "$cold" "$warm"
done

echo
echo "Go benchmarks (layer/cache):"
(cd "$ROOT" && go test -bench=. -benchmem ./layer ./cache 2>/dev/null | grep -E 'Benchmark|ok ')

if command -v docker >/dev/null 2>&1; then
  echo
  echo "Docker comparison (100 files, if Docker available):"
  docker_ctx="${WORKDIR}/docker-ctx"
  rm -rf "$docker_ctx"
  mkdir -p "${docker_ctx}/files"
  for i in $(seq 1 100); do
    echo "payload-${i}" >"${docker_ctx}/files/file-${i}.txt"
  done
  cat >"${docker_ctx}/Dockerfile" <<'EOF'
FROM alpine:latest
COPY files /data/
CMD ["/bin/sh"]
EOF
  docker build -t bench-docker:cold "${docker_ctx}" -q
  start=$(date +%s.%N)
  docker build -t bench-docker:cold "${docker_ctx}" -q
  end=$(date +%s.%N)
  docker_cold=$(awk -v s="$start" -v e="$end" 'BEGIN { printf "%.3f", e - s }')
  start=$(date +%s.%N)
  docker build -t bench-docker:warm "${docker_ctx}" -q
  end=$(date +%s.%N)
  docker_warm=$(awk -v s="$start" -v e="$end" 'BEGIN { printf "%.3f", e - s }')
  printf "Docker cold/warm (100 files): %s / %s seconds\n" "$docker_cold" "$docker_warm"
fi

rm -rf "$WORKDIR"
