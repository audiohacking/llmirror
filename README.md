# llmirror

Share Hugging Face model downloads across your fleet. Before hitting huggingface.co, **llmirror** checks your local HF cache and copies from peers on the LAN.

Single portable binary. Same cache paths as `huggingface-cli` / `transformers` / `vLLM`. No central server.

**Download resolution:** local cache → fleet peer (exact revision) → Hugging Face.

## Install

Grab the latest binary from **[Releases](https://github.com/audiohacking/llmirror/releases)** — no Go toolchain required.

| Platform | Asset |
|----------|--------|
| Linux x86_64 | `llmirror-linux-amd64` |
| Linux ARM64 | `llmirror-linux-arm64` |
| macOS Apple Silicon | `llmirror-darwin-arm64` |

```bash
# Example: Linux amd64
curl -fsSL -o llmirror \
  https://github.com/audiohacking/llmirror/releases/latest/download/llmirror-linux-amd64
chmod +x llmirror
sudo mv llmirror /usr/local/bin/   # optional
llmirror version
```

## Run as a service (recommended)

One command installs a background peer on Linux (systemd user unit) or macOS (LaunchAgent), generates a fleet token, and enables secure defaults:

```bash
llmirror install-service
# share ~/.config/llmirror/token with other hosts (same token on every machine)
# optional: edit ~/.config/llmirror/peers with http://gpu-01:7947 lines

# later
llmirror uninstall-service
```

On Linux, keep the user service alive after logout:

```bash
loginctl enable-linger "$USER"
```

## Practical example: two GPU hosts

Goal: **gpu-01** already has a model. **gpu-02** should get it from gpu-01 over the LAN instead of downloading from Hugging Face again.

### 1. Install + service on both hosts

```bash
# on each host
curl -fsSL -o llmirror \
  https://github.com/audiohacking/llmirror/releases/latest/download/llmirror-linux-amd64
chmod +x llmirror && sudo mv llmirror /usr/local/bin/

llmirror install-service
```

Copy the **same** token and group to every host:

```bash
# from gpu-01
scp ~/.config/llmirror/token gpu-02:~/.config/llmirror/token
scp ~/.config/llmirror/group gpu-02:~/.config/llmirror/group
# restart service on gpu-02 after copying
systemctl --user restart llmirror   # Linux
# launchctl kickstart -k gui/$(id -u)/com.audiohacking.llmirror   # macOS
```

If mDNS is blocked, pin peers:

```bash
cat > ~/.config/llmirror/peers <<'EOF'
http://gpu-01:7947
http://gpu-02:7947
EOF
```

### 2. On gpu-01 — log in to Hugging Face, then download once

Authenticated Hub access is required for gated models and is recommended for best download performance:

```bash
# one-time per host (or export HF_TOKEN=hf_...)
hf auth login
# optional: max Xet transfer performance
export HF_XET_HIGH_PERFORMANCE=1

llmirror download meta-llama/Llama-3.1-8B-Instruct
llmirror scan
```

### 3. On gpu-02 — pull from the fleet

```bash
llmirror download meta-llama/Llama-3.1-8B-Instruct
# or force peers-only:
llmirror download meta-llama/Llama-3.1-8B-Instruct --skip-hf
```

### 4. Use the model as usual

```python
from transformers import AutoModelForCausalLM
model = AutoModelForCausalLM.from_pretrained("meta-llama/Llama-3.1-8B-Instruct")
```

---

## Security (defaults that avoid becoming a boomerang)

llmirror is meant for **trusted private networks**, not the public Internet.

| Control | Default |
|---------|---------|
| Network ACL | Only loopback + RFC1918 + link-local (+ IPv6 ULA). Public clients get `403`. |
| Spoofing | `X-Forwarded-For` / `X-Real-IP` are **ignored** |
| Fleet group | Isolates mDNS discovery + HTTP (`X-Llmirror-Group`). `install-service` generates one. |
| Fleet token | Optional but **strongly recommended**; `install-service` generates one. Header: `X-Llmirror-Token` |
| `cdn-proxy` bind | `127.0.0.1:7950` (loopback only) |
| Blob paths | Hash allowlist only (`[a-f0-9]{40,64}`) |
| Escape hatch | `--allow-public` (prints a loud warning) — avoid |

Hosts without a matching **group** never see each other on mDNS, and HTTP pulls from another group are rejected even if listed in `peers`. Share both `token` and `group` across your fleet (or set `--group lab-a` explicitly).

```bash
# serve with explicit extra private range (e.g. lab VPC)
llmirror serve --allow 10.10.0.0/16 --token-file ~/.config/llmirror/token
```

## Hugging Face login & tokens

llmirror falls back to the official Hub (`hf download` / `cdn-proxy` upstream). Use a Hub token so gated models work and authenticated transfers stay fast.

```bash
# Interactive (stores token under HF_HOME, usually ~/.cache/huggingface/token)
hf auth login

# Or non-interactive / CI / systemd user env
export HF_TOKEN=hf_your_token_here
# create at: https://huggingface.co/settings/token  (read access is enough for downloads)

hf auth whoami
```

Tips:

| Tip | Why |
|-----|-----|
| `export HF_TOKEN=…` | Overrides stored login; best for services and CI |
| `export HF_XET_HIGH_PERFORMANCE=1` | Higher-throughput Hub/Xet transfers when falling back to HF (needs ample RAM) |
| Accept model licenses on the Hub website | Gated repos (Llama, etc.) still need one-time license acceptance |
| Same `HF_TOKEN` on the host running `cdn-proxy` | Proxy injects it on upstream requests if the client omitted `Authorization` |

Fleet peer copies do **not** need an HF token — only the first host that pulls from huggingface.co does.

Do not confuse **`HF_TOKEN`** (Hugging Face Hub) with **`LLMIRROR_TOKEN`** (your private fleet auth).

## Optional: drop-in `hf` alias

```bash
alias hf='llmirror proxy -- hf'
hf download meta-llama/Llama-3.1-8B-Instruct
```

## Optional: transparent proxy for Python libs

```bash
llmirror cdn-proxy          # listens on 127.0.0.1:7950
export HF_ENDPOINT=http://127.0.0.1:7950
python my_train.py
```

Supports model, dataset, and space resolve/raw URLs. Responses include `X-Llmirror-Source: local|peer|upstream`.

## Commands

| Command | Description |
|---------|-------------|
| `install-service` | systemd / launchd with secure defaults + token |
| `uninstall-service` | Remove the background service |
| `serve` | Share HF cache on LAN (`:7947`, private ACL) |
| `download REPO_ID` | Local → peers → HF |
| `scan` | List cached models/datasets/spaces |
| `peers` | Discover peers |
| `cdn-proxy` | Loopback `HF_ENDPOINT` proxy |
| `proxy -- hf …` | HF CLI alias wrapper |
| `version` | Print build version |

## Environment

| Variable | Effect |
|----------|--------|
| `HF_HUB_CACHE` / `HF_HOME` / `XDG_CACHE_HOME` | HF cache location |
| `HF_TOKEN` / `HUGGING_FACE_HUB_TOKEN` | Hugging Face Hub auth (gated models + authenticated downloads) |
| `HF_XET_HIGH_PERFORMANCE` | Higher-throughput Hub transfers (`=1`) |
| `HF_ENDPOINT` | Point at `cdn-proxy` |
| `LLMIRROR_PEERS` | Static peer list path |
| `LLMIRROR_TOKEN` / `LLMIRROR_TOKEN_FILE` | Shared fleet auth |
| `LLMIRROR_GROUP` / `LLMIRROR_GROUP_FILE` | Fleet group id (mDNS + HTTP isolation) |

## Build from source

```bash
git clone https://github.com/audiohacking/llmirror.git
cd llmirror
make build && make test
make test-integration
```

## License

[Apache License 2.0](LICENSE)
