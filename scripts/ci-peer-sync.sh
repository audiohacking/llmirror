#!/usr/bin/env bash
# Integration test: seed peer A from Hugging Face, serve it, pull on peer B via llmirror only.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LLMIRROR="${LLMIRROR:-$ROOT/llmirror}"
MODEL="${LLMIRROR_TEST_MODEL:-hf-internal-testing/tiny-random-gpt2}"
REVISION="${LLMIRROR_TEST_REVISION:-main}"
PORT_A="${LLMIRROR_PORT_A:-17947}"

if [[ ! -x "$LLMIRROR" ]]; then
  echo "building llmirror..."
  (cd "$ROOT" && go build -o llmirror ./cmd/llmirror)
  LLMIRROR="$ROOT/llmirror"
fi

if ! command -v hf >/dev/null 2>&1 && ! command -v huggingface-cli >/dev/null 2>&1; then
  echo "error: hf or huggingface-cli required to seed peer A" >&2
  exit 1
fi

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/llmirror-ci.XXXXXX")"
cleanup() {
  if [[ -n "${PID_A:-}" ]] && kill -0 "$PID_A" 2>/dev/null; then
    kill "$PID_A" 2>/dev/null || true
    wait "$PID_A" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

CACHE_A="$WORKDIR/cache-a"
CACHE_B="$WORKDIR/cache-b"
CACHE_C="$WORKDIR/cache-c"
PEERS="$WORKDIR/peers"
mkdir -p "$CACHE_A" "$CACHE_B" "$CACHE_C"
echo "http://127.0.0.1:${PORT_A}" >"$PEERS"

echo "==> [A] download ${MODEL}@${REVISION} from Hugging Face into isolated cache"
HF_HUB_CACHE="$CACHE_A" "$LLMIRROR" download "$MODEL" --revision "$REVISION" --skip-peers

echo "==> [A] scan cache"
HF_HUB_CACHE="$CACHE_A" "$LLMIRROR" scan | tee "$WORKDIR/scan-a.txt"
grep -q "$MODEL" "$WORKDIR/scan-a.txt"

echo "==> [A] serve on :${PORT_A}"
HF_HUB_CACHE="$CACHE_A" "$LLMIRROR" serve --addr ":${PORT_A}" >"$WORKDIR/serve-a.log" 2>&1 &
PID_A=$!

echo -n "==> wait for peer A"
for _ in $(seq 1 50); do
  if curl -sf "http://127.0.0.1:${PORT_A}/healthz" >/dev/null; then
    echo " ready"
    break
  fi
  if ! kill -0 "$PID_A" 2>/dev/null; then
    echo
    echo "peer A exited early:" >&2
    cat "$WORKDIR/serve-a.log" >&2
    exit 1
  fi
  echo -n "."
  sleep 0.1
done
curl -sf "http://127.0.0.1:${PORT_A}/v1/models" | tee "$WORKDIR/models-a.json" >/dev/null

echo "==> [A] has-revision check"
HAS_URL="http://127.0.0.1:${PORT_A}/v1/models/${MODEL}/has?revision=${REVISION}"
curl -sf "$HAS_URL" | tee "$WORKDIR/has-a.json"
grep -q '"present":true' "$WORKDIR/has-a.json"

echo "==> [B] download from peer A only (--skip-hf, empty cache)"
HF_HUB_CACHE="$CACHE_B" "$LLMIRROR" download "$MODEL" \
  --revision "$REVISION" \
  --peers-file "$PEERS" \
  --skip-hf

echo "==> [B] verify model present"
HF_HUB_CACHE="$CACHE_B" "$LLMIRROR" scan | tee "$WORKDIR/scan-b.txt"
grep -q "$MODEL" "$WORKDIR/scan-b.txt"

# Snapshot trees should exist on both sides
FOLDER="models--${MODEL//\//--}"
test -d "$CACHE_A/$FOLDER/snapshots"
test -d "$CACHE_B/$FOLDER/snapshots"
# At least one blob copied
test "$(find "$CACHE_B/$FOLDER/blobs" -type f ! -name '*.partial' | wc -l | tr -d ' ')" -ge 1

echo "==> [C] negative: empty cache + skip-hf + no peers must fail"
if HF_HUB_CACHE="$CACHE_C" "$LLMIRROR" download "$MODEL" \
  --revision "$REVISION" \
  --peers-file /dev/null \
  --skip-peers \
  --skip-hf; then
  echo "error: expected download to fail without peers/HF" >&2
  exit 1
fi

echo "==> peer sync OK (${MODEL}@${REVISION})"
