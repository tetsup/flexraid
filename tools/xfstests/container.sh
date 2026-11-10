#!/usr/bin/env bash
set -euo pipefail

XFSTESTS_DIR=/opt/xfstests
REPO_DIR=/workspace/flexraid
TOOL_DIR="$REPO_DIR/tools/xfstests"
DATA_DIR=/xfstests-data
RESULTS_DIR="$DATA_DIR/results"
MOUNT_DIR="$DATA_DIR/mount"
SCRATCH_DIR="$DATA_DIR/scratch"
CONFIG_FILE="$DATA_DIR/flexraid.yaml"
FLEXRAID_BIN=/tmp/flexraid
PID_FILE="$DATA_DIR/flexraid.pid"
LOG_FILE="$DATA_DIR/flexraid-mount.log"
RC_FILE="$REPO_DIR/.github/xfstests/flexraid.rc"

prepare() {
  mkdir -p "$DATA_DIR/disk1" "$DATA_DIR/disk2" "$DATA_DIR/disk3" "$DATA_DIR/disk4"
  mkdir -p "$MOUNT_DIR" "$SCRATCH_DIR" "$RESULTS_DIR"

  cat >"$CONFIG_FILE" <<EOF
disks:
  - "$DATA_DIR/disk1"
  - "$DATA_DIR/disk2"
  - "$DATA_DIR/disk3"
  - "$DATA_DIR/disk4"
mountpoint: "$MOUNT_DIR"
EOF

  cd "$REPO_DIR"
  go build -o "$FLEXRAID_BIN" ./main

  cat >"$XFSTESTS_DIR/local.config" <<EOF
export TEST_DIR="$MOUNT_DIR"
export TEST_DEV=flexraid-fuse
export SCRATCH_DIR="$SCRATCH_DIR"
export FSTYP=fuse
export RESULTS_DIR="$RESULTS_DIR"
export FLEXRAID_BIN="$FLEXRAID_BIN"
export FLEXRAID_CONFIG="$CONFIG_FILE"
export FLEXRAID_PID_FILE="$PID_FILE"
export FLEXRAID_LOG="$LOG_FILE"
export FLEXRAID_XFSTESTS_RC="$RC_FILE"
EOF

  if ! grep -qF '# FLEXRAID_XFSTESTS_RC hook' "$XFSTESTS_DIR/common/rc"; then
    cat >>"$XFSTESTS_DIR/common/rc" <<'EOF'

# FLEXRAID_XFSTESTS_RC hook
if [ -n "${FLEXRAID_XFSTESTS_RC:-}" ]; then
  . "$FLEXRAID_XFSTESTS_RC"
fi
EOF
  fi
}

list_generic() {
  awk '
    {
      sub(/[[:space:]]*#.*/, "")
      if (NF < 2) next
      for (i = 2; i <= NF; i++) {
        if ($i == "auto") {
          print "generic/" $1
          break
        }
      }
    }
  ' "$XFSTESTS_DIR/tests/generic/group.list"
}

run_tests() {
  local -a tests=("$@")
  local stamp log status
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  log="$RESULTS_DIR/xfstests-$stamp.log"

  cd "$XFSTESTS_DIR"
  source ./local.config
  echo "Running: ${tests[*]}"
  echo "Log: $log"

  set +e
  bash ./check "${tests[@]}" 2>&1 | tee "$log"
  status=${PIPESTATUS[0]}
  set -e
  printf '%s\n' "$status" >"$RESULTS_DIR/xfstests-$stamp.exit-status"
  return "$status"
}

command="${1:-help}"
if [ "$#" -gt 0 ]; then shift; fi

case "$command" in
  help|-h|--help)
    cat <<'EOF'
Container commands:
  shell        Open an interactive shell after preparing the environment
  list         List generic tests tagged "auto"
  run TEST...  Run the named xfstests tests
  run-generic  Run all generic tests tagged "auto"
EOF
    ;;
  shell)
    prepare
    exec bash -i
    ;;
  list)
    list_generic
    ;;
  run)
    if [ "$#" -eq 0 ]; then
      echo "run requires at least one test name" >&2
      exit 2
    fi
    prepare
    run_tests "$@"
    ;;
  run-generic)
    prepare
    mapfile -t tests < <(list_generic)
    if [ "${#tests[@]}" -eq 0 ]; then
      echo "no generic tests tagged auto were found" >&2
      exit 1
    fi
    run_tests "${tests[@]}"
    ;;
  *)
    echo "unknown container command: $command" >&2
    exit 2
    ;;
esac
