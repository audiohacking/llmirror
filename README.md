# llmirror

P2P mirror for Hugging Face model caches across your fleet. Before downloading from huggingface.co, **llmirror** checks the local HF cache and copies from peers on your LAN.

Single static binary, no external coordination service. Models land in the same locations as `huggingface-cli` (`HF_HUB_CACHE`, `HF_HOME`, or `~/.cache/huggingface/hub`).

## Problem

Every GPU host in your fleet runs `hf download meta-llama/Llama-3-70B` independently. Even when a neighbor already has the full model, you pay the bandwidth and wait again.

## Solution

```
┌─────────────┐     mDNS / static peers     ┌─────────────┐
│   host-a    │◄───────────────────────────►│   host-b    │
│ llmirror    │     HTTP blob transfer      │ llmirror    │
│ serve       │                             │ serve       │
└──────┬──────┘                             └──────┬──────┘
       │                                           │
       ▼                                           ▼
 ~/.cache/huggingface/hub              ~/.cache/huggingface/hub
 (same layout as HF)                   (same layout as HF)
```

**Download resolution order:**

1. **Local** — already in HF hub cache?
2. **Peer** — another host has the *exact* revision? Copy blobs (resumable) into your cache.
3. **Hugging Face** — fall back to `hf download` / upstream via CDN proxy.

## Quick start

```bash
# Build (or grab a release binary)
make build

# On every host that should share models
./llmirror serve

# Download — peers first, HF last
./llmirror download meta-llama/Llama-3.1-8B-Instruct
```

## HF compatibility

### Drop-in alias (recommended)

```bash
# ~/.bashrc or fleet bootstrap script
alias hf='llmirror proxy -- hf'
hf download org/model --revision main
```

`proxy` intercepts `hf download …` and runs the peer/local path. Other `hf` subcommands pass through to the real CLI.

### Transparent CDN proxy (Python libs)

Libraries that talk to the Hub via `huggingface_hub` respect `HF_ENDPOINT`. Point it at llmirror so `transformers`, `vllm`, etc. get local/peer hits without changing code:

```bash
# Terminal A — reverse proxy for Hub resolve/raw URLs
./llmirror cdn-proxy --addr :7950

# Terminal B — same host or any client
export HF_ENDPOINT=http://127.0.0.1:7950
python -c "from transformers import AutoModel; AutoModel.from_pretrained('org/model')"
```

Resolution for `/{repo}/resolve/{rev}/{file}`:

1. Local HF cache  
2. Fleet peer with that exact revision  
3. Upstream `https://huggingface.co` (follows CDN redirects)

Responses include `X-Llmirror-Source: local|peer|upstream`.

### Direct replacement

```bash
llmirror download org/model [--revision REV] [extra hf flags...]
```

Uses the same env vars as [huggingface_hub](https://huggingface.co/docs/huggingface_hub/en/package_reference/environment_variables):

| Variable | Effect |
|----------|--------|
| `HF_HUB_CACHE` | Hub cache directory (highest priority) |
| `HF_HOME` | Base dir; cache at `$HF_HOME/hub` |
| `XDG_CACHE_HOME` | Linux fallback when `HF_HOME` unset |
| `HF_ENDPOINT` | Point at `llmirror cdn-proxy` for transparent hijack |

### Static peers (optional)

mDNS works on LANs where multicast is allowed. For routed networks or fixed topology, list peers explicitly:

```bash
# ~/.config/llmirror/peers  (or LLMIRROR_PEERS env)
http://gpu-01.local:7947
http://10.0.0.42:7947
```

## Commands

| Command | Description |
|---------|-------------|
| `serve` | Expose local HF cache over HTTP; advertise via mDNS |
| `cdn-proxy` | `HF_ENDPOINT` reverse proxy (local → peers → Hub) |
| `download REPO_ID` | Local → peers → HF |
| `scan` | List models in local cache |
| `peers` | Discover fleet peers |
| `proxy -- hf …` | Alias wrapper for HF CLI |

## How peer copy works

llmirror speaks **native HF hub cache layout**:

- Repo folders: `models--org--name`
- Content-addressed `blobs/`
- `snapshots/` symlinks (copies on Windows)
- `refs/` for branch/tag → commit mapping

**Revision-aware matching:** peers are queried via `/v1/models/{repo}/has?revision=…` so a host with `main` at commit A is not used when you need commit B.

**Partial sync:** interrupted blob transfers leave `blobs/<hash>.partial` and resume with HTTP `Range` requests on the next attempt.

After import, `transformers`, `vllm`, etc. see the model as if you downloaded it locally.

## Cross-platform builds

Release binaries (Linux amd64/arm64, macOS arm64) are built and attached automatically when a GitHub Release is published.

```bash
make build-all   # linux-amd64, linux-arm64, darwin-arm64
```

## Architecture notes

| Component | Role |
|-----------|------|
| `internal/cache` | HF path detection, cache scan, resumable snapshot import |
| `internal/peer` | mDNS discovery, revision-aware HTTP server/client |
| `internal/cdnproxy` | `HF_ENDPOINT` reverse proxy for Python libs |
| `internal/download` | Resolution orchestration |
| `internal/hf` | Fallback to official CLI |

**Future directions:**

- TLS + auth between peers
- systemd/launchd service templates for `serve` / `cdn-proxy`
- Dataset/space cache folder support in the CDN proxy

## License

[Apache License 2.0](LICENSE)
