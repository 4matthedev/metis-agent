#!/usr/bin/env bash
# Metis Agent installer.
#
#   curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash
#   curl -fsSL ... | bash -s -- --advanced
#
# Easy mode (default) installs Ollama + gemma4:e2b, installs Metis, and writes a
# global config so `metis-agent` works from any directory (including $HOME).
#
# Advanced mode lets you pick Ollama, llama.cpp, or LM Studio, reuses a model
# you already have, and offers gemma4:e2b when none is found.
#
# The script is safe to re-run: the binary and launcher are replaced, and an
# existing config is backed up before it is rewritten.
set -euo pipefail

REPO="4matthedev/metis-agent"
DEFAULT_MODEL="gemma4:e2b"
GGUF_REPO="ggml-org/gemma-4-E2B-it-GGUF"
GGUF_FILE="gemma-4-E2B-it-Q4_0.gguf"
GGUF_URL="https://huggingface.co/${GGUF_REPO}/resolve/main/${GGUF_FILE}"
LLAMA_ALIAS="gemma-4-e2b"
LLAMA_PORT=8080
OLLAMA_PORT=11434
LMSTUDIO_PORT=1234

MODE=""
BACKEND=""
MODEL=""
MODEL_EXPLICIT=0
ASSUME_YES=0
DRY_RUN=0
PREFIX=""
VERSION="latest"
INSTALL_BACKEND=1
PULL_MODEL=1

# --- output -----------------------------------------------------------------
if [ -t 1 ]; then
  C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'; C_GREEN=$'\033[32m'
  C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_DIM=$'\033[2m'
else
  C_RESET=""; C_BOLD=""; C_GREEN=""; C_YELLOW=""; C_RED=""; C_DIM=""
fi
say()  { printf '%s\n' "$*"; }
ok()   { printf '%s✓%s %s\n' "$C_GREEN" "$C_RESET" "$*"; }
warn() { printf '%s!%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2; }
die()  { printf '%s✗%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; exit 1; }
step() { printf '\n%s==>%s %s%s%s\n' "$C_GREEN" "$C_RESET" "$C_BOLD" "$*" "$C_RESET"; }

# run a command, or print it in dry-run mode
run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '%s  + %s%s\n' "$C_DIM" "$*" "$C_RESET"
    return 0
  fi
  "$@"
}

# --- temp files -------------------------------------------------------------
TMP_DIRS=()
cleanup() {
  local d
  for d in "${TMP_DIRS[@]:-}"; do
    [ -n "$d" ] && rm -rf "$d"
  done
}
trap cleanup EXIT

mktemp_dir() {
  local d
  d="$(mktemp -d)"
  TMP_DIRS+=("$d")
  printf '%s' "$d"
}

# --- prompts ----------------------------------------------------------------
# Prompts read from /dev/tty so the script still works when piped to bash.
TTY_FD=0
if (exec 3</dev/tty) 2>/dev/null; then
  exec 3</dev/tty
  TTY_FD=3
fi

ask_yes_no() { # prompt default(y/n) -> 0 for yes
  local prompt="$1" default="${2:-y}" answer=""
  if [ "$ASSUME_YES" -eq 1 ] || [ "$TTY_FD" -eq 0 ]; then
    [ "$default" = "y" ]
    return
  fi
  local hint="[Y/n]"; [ "$default" = "n" ] && hint="[y/N]"
  printf '%s %s ' "$prompt" "$hint" >&"$TTY_FD"
  IFS= read -r answer <&"$TTY_FD" || answer=""
  answer="${answer:-$default}"
  case "$answer" in y|Y|yes|YES) return 0;; *) return 1;; esac
}

ask_choice() { # prompt, default index, options... -> echoes index
  local prompt="$1" def="$2"; shift 2
  local -a opts=("$@")
  local i choice
  if [ "$ASSUME_YES" -eq 1 ] || [ "$TTY_FD" -eq 0 ]; then
    printf '%s' "$def"; return 0
  fi
  printf '%s\n' "$prompt" >&"$TTY_FD"
  for i in "${!opts[@]}"; do
    printf '  %d) %s\n' "$((i+1))" "${opts[$i]}" >&"$TTY_FD"
  done
  printf 'Choice [%s]: ' "$def" >&"$TTY_FD"
  IFS= read -r choice <&"$TTY_FD" || choice=""
  choice="${choice:-$def}"
  printf '%s' "$choice"
}

# --- arguments --------------------------------------------------------------
usage() {
  cat <<EOF
Metis Agent installer

Usage:
  install.sh [options]

Options:
  --easy              Install Ollama + ${DEFAULT_MODEL} and configure Metis (default)
  --advanced          Choose the backend and model interactively
  --backend NAME      ollama | llamacpp | lmstudio | none
  --model NAME        Model to use (overrides auto-detection)
  --no-model          Do not download/pull a model
  --no-backend        Install Metis only (same as --backend none)
  --version TAG       Install a specific release (e.g. 0.1.0; default: latest)
  --prefix DIR        Install prefix (default: \$HOME/.local)
  --yes, -y           Assume yes for all prompts
  --dry-run           Print actions without changing anything
  -h, --help          Show this help

Examples:
  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | bash
  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | bash -s -- --advanced
  ./install.sh --advanced --backend llamacpp
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --easy) MODE=easy ;;
    --advanced) MODE=advanced ;;
    --backend) shift; BACKEND="${1:-}" ;;
    --model) shift; MODEL="${1:-}"; MODEL_EXPLICIT=1 ;;
    --no-model) PULL_MODEL=0 ;;
    --no-backend) BACKEND=none; INSTALL_BACKEND=0 ;;
    --version) shift; VERSION="${1:-latest}" ;;
    --prefix) shift; PREFIX="${1:-}" ;;
    --yes|-y) ASSUME_YES=1 ;;
    --dry-run) DRY_RUN=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
  shift
done

case "$BACKEND" in
  ""|ollama|llamacpp|lmstudio|none) ;;
  llama.cpp|llama) BACKEND=llamacpp ;;
  lm-studio|lm_studio|lmstudio-cli) BACKEND=lmstudio ;;
  *) die "unknown backend: $BACKEND" ;;
esac

# --- platform ---------------------------------------------------------------
OS="$(uname -s)"
ARCH="$(uname -m)"
case "$OS" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "unsupported OS: $OS (Linux and macOS are supported)" ;;
esac
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

[ -n "$PREFIX" ] || PREFIX="$HOME/.local"
BIN_DIR="$PREFIX/bin"
# Match internal/config: os.UserConfigDir on macOS is ~/Library/Application
# Support (XDG is ignored), and DataDir is the same directory there. On Linux,
# honour XDG_CONFIG_HOME and XDG_DATA_HOME.
if [ "$OS" = "darwin" ]; then
  APP_SUPPORT="$HOME/Library/Application Support"
  DATA_DIR="$APP_SUPPORT/metis-agent"
  CONFIG_DIR="$APP_SUPPORT/metis-agent"
else
  DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/metis-agent"
  CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/metis-agent"
fi
CONFIG_FILE="$CONFIG_DIR/config.json"
LOG_FILE="$DATA_DIR/backend.log"
WRAPPER="$BIN_DIR/metis-agent"
REAL_BIN="$DATA_DIR/bin/metis-agent"

need() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }

step "Metis Agent installer"
say "platform: ${OS}/${ARCH}"
say "prefix:   ${PREFIX}"
say "data:     ${DATA_DIR}"
say "config:   ${CONFIG_FILE}"
[ "$DRY_RUN" -eq 1 ] && warn "dry run: nothing will be installed"

need curl
need tar

run mkdir -p "$BIN_DIR" "$DATA_DIR/bin" "$CONFIG_DIR" "$DATA_DIR/models"

# --- metis binary -----------------------------------------------------------
# sha256 checker that works on Linux and macOS.
check_sha256() { # dir asset
  local dir="$1" asset="$2"
  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$dir" && grep " ${asset}\$" SHA256SUMS | sha256sum -c - >/dev/null 2>&1 )
  elif command -v shasum >/dev/null 2>&1; then
    ( cd "$dir" && grep " ${asset}\$" SHA256SUMS | shasum -a 256 -c - >/dev/null 2>&1 )
  else
    return 1
  fi
}

install_metis() {
  step "Installing metis-agent"
  local tmp asset url sums
  tmp="$(mktemp_dir)"

  if [ "$VERSION" = "latest" ]; then
    asset="metis-agent_${OS}_${ARCH}.tar.gz"
    url="https://github.com/${REPO}/releases/latest/download/${asset}"
    sums="https://github.com/${REPO}/releases/latest/download/SHA256SUMS"
  else
    local v="${VERSION#v}"
    asset="metis-agent_${v}_${OS}_${ARCH}.tar.gz"
    url="https://github.com/${REPO}/releases/download/v${v}/${asset}"
    sums="https://github.com/${REPO}/releases/download/v${v}/SHA256SUMS"
  fi

  if [ "$DRY_RUN" -eq 1 ]; then
    printf '%s  + download %s%s\n' "$C_DIM" "$url" "$C_RESET"
    printf '%s  + install metis-agent -> %s%s\n' "$C_DIM" "$REAL_BIN" "$C_RESET"
    return 0
  fi

  say "downloading ${url}"
  if curl -fsSL "$url" -o "$tmp/$asset"; then
    if curl -fsSL "$sums" -o "$tmp/SHA256SUMS" 2>/dev/null; then
      if check_sha256 "$tmp" "$asset"; then
        ok "checksum verified"
      else
        warn "checksum not verified; continuing"
      fi
    fi
    tar -xzf "$tmp/$asset" -C "$tmp" || die "could not extract ${asset}"
    [ -f "$tmp/metis-agent" ] || die "archive did not contain metis-agent"
    install -m 0755 "$tmp/metis-agent" "$REAL_BIN"
    ok "installed $REAL_BIN"
    return 0
  fi

  # Fallback: build from source when a release is unavailable.
  if command -v go >/dev/null 2>&1; then
    warn "release download failed; building from source with Go"
    need git
    git clone --depth 1 "https://github.com/${REPO}.git" "$tmp/src" >/dev/null 2>&1 \
      || die "could not clone https://github.com/${REPO}"
    ( cd "$tmp/src" && go build -trimpath -o "$REAL_BIN" . )
    ok "built $REAL_BIN from source"
    return 0
  fi

  die "could not download a release and Go is not installed. Install Go, or download a release manually: https://github.com/${REPO}/releases"
}

# --- backend helpers --------------------------------------------------------
have() { command -v "$1" >/dev/null 2>&1; }

ollama_healthy() { curl -fsS --max-time 2 "http://127.0.0.1:${OLLAMA_PORT}/api/tags" >/dev/null 2>&1; }
llamacpp_healthy() { curl -fsS --max-time 2 "http://127.0.0.1:${LLAMA_PORT}/v1/models" >/dev/null 2>&1; }
lmstudio_healthy() { curl -fsS --max-time 2 "http://127.0.0.1:${LMSTUDIO_PORT}/v1/models" >/dev/null 2>&1; }

find_lms() {
  if have lms; then command -v lms; return 0; fi
  local candidates=(
    "$HOME/.lmstudio/bin/lms"
    "/Applications/LM Studio.app/Contents/Resources/app/.webpack/lms"
    "$HOME/.cache/lm-studio/bin/lms"
  )
  local c
  for c in "${candidates[@]}"; do [ -x "$c" ] && { printf '%s' "$c"; return 0; }; done
  return 1
}

ollama_models() {
  ollama list 2>/dev/null | awk 'NR>1 && $1 != "" {print $1}'
}

lmstudio_models() {
  "$1" ls --llm 2>/dev/null | awk '
    /PARAMS/ {inlist=1; next}
    inlist && NF==0 {exit}
    inlist && NF>0 {print $1}
  '
}

# find_gguf echo the first usable GGUF, if any.
find_gguf() {
  local dirs=(
    "$DATA_DIR/models"
    "$HOME/models"
    "$HOME/.local/share/llama.cpp/models"
    "$HOME/.cache/lm-studio/models"
    "$HOME/.lmstudio/models"
  )
  local d f
  for d in "${dirs[@]}"; do
    [ -d "$d" ] || continue
    f="$(find "$d" -type f -name '*.gguf' ! -name 'mmproj-*' ! -name 'mtp-*' 2>/dev/null | head -n1)"
    [ -n "$f" ] && { printf '%s' "$f"; return 0; }
  done
  return 1
}

install_ollama() {
  step "Installing Ollama"
  if have ollama; then ok "Ollama is already installed"; return 0; fi
  if [ "$OS" = "linux" ]; then
    say "running the official Ollama installer (it may ask for sudo)"
    if [ "$DRY_RUN" -eq 1 ]; then
      printf '%s  + curl -fsSL https://ollama.com/install.sh | sh%s\n' "$C_DIM" "$C_RESET"
      return 0
    fi
    curl -fsSL https://ollama.com/install.sh | sh
  elif [ "$OS" = "darwin" ]; then
    if have brew; then
      run brew install ollama
    else
      die "install Ollama from https://ollama.com/download, then re-run this installer"
    fi
  fi
  have ollama || die "Ollama install did not produce an ollama binary"
  ok "Ollama installed"
}

start_ollama() {
  if ollama_healthy; then ok "Ollama is running"; return 0; fi
  step "Starting Ollama"
  if [ "$OS" = "linux" ] && have systemctl; then
    systemctl start ollama >/dev/null 2>&1 \
      || sudo -n systemctl start ollama >/dev/null 2>&1 \
      || true
  elif [ "$OS" = "darwin" ] && have brew; then
    brew services start ollama >/dev/null 2>&1 || true
  fi
  local i
  for ((i = 0; i < 20; i++)); do ollama_healthy && break; sleep 0.5; done
  if ! ollama_healthy && [ "$DRY_RUN" -eq 0 ]; then
    say "starting Ollama in the background"
    nohup ollama serve >>"$LOG_FILE" 2>&1 &
  fi
  for ((i = 0; i < 120; i++)); do ollama_healthy && break; sleep 0.5; done
  ollama_healthy || warn "Ollama is not answering yet; the launcher will start it when needed"
}

# select_ollama_model sets MODEL for the ollama backend.
select_ollama_model() {
  local installed first
  installed="$(ollama_models)"
  if [ "$MODEL_EXPLICIT" -eq 0 ] && [ "$MODE" = "easy" ]; then
    MODEL="$DEFAULT_MODEL"
  fi
  if [ -n "$MODEL" ]; then
    if [ "$PULL_MODEL" -eq 0 ]; then
      say "model: ${MODEL} (not pulling; --no-model)"
      return 0
    fi
    if printf '%s\n' "$installed" | grep -qx "$MODEL"; then
      ok "model ${MODEL} is already installed"
      return 0
    fi
    step "Pulling ${MODEL}"
    say "this downloads several GB on first run"
    run ollama pull "$MODEL"
    return 0
  fi

  if [ -n "$installed" ]; then
    first="$(printf '%s\n' "$installed" | head -n1)"
    if [ "$TTY_FD" -ne 0 ] && [ "$ASSUME_YES" -eq 0 ]; then
      local -a opts=()
      while IFS= read -r line; do [ -n "$line" ] && opts+=("$line"); done <<<"$installed"
      opts+=("Pull ${DEFAULT_MODEL} instead")
      local choice idx
      choice="$(ask_choice "Which Ollama model should Metis use?" 1 "${opts[@]}")"
      idx=$((choice-1))
      if [ "$idx" -ge 0 ] && [ "$idx" -lt "${#opts[@]}" ] && [ "$idx" -ne $((${#opts[@]}-1)) ]; then
        MODEL="${opts[$idx]}"
      elif [ "$idx" -eq $((${#opts[@]}-1)) ]; then
        MODEL="$DEFAULT_MODEL"
        run ollama pull "$MODEL"
      else
        MODEL="$first"
      fi
    else
      MODEL="$first"
    fi
    ok "using existing model ${MODEL}"
    return 0
  fi

  if [ "$PULL_MODEL" -eq 1 ] && ask_yes_no "No Ollama model found. Pull ${DEFAULT_MODEL} (several GB)?" y; then
    MODEL="$DEFAULT_MODEL"
    step "Pulling ${MODEL}"
    run ollama pull "$MODEL"
  else
    MODEL=""
    warn "no model configured; pass --model later or run: ollama pull ${DEFAULT_MODEL}"
  fi
}

setup_ollama() {
  install_ollama
  start_ollama
  select_ollama_model
  BACKEND=ollama
}

install_llamacpp() {
  step "Installing llama.cpp"
  LLAMA_SERVER=""
  if have llama-server; then
    LLAMA_SERVER="$(command -v llama-server)"
    ok "llama-server is already installed"
    return 0
  fi
  if [ "$OS" = "darwin" ] && have brew; then
    run brew install llama.cpp
    LLAMA_SERVER="$(command -v llama-server || true)"
    [ -n "$LLAMA_SERVER" ] && { ok "llama.cpp installed"; return 0; }
  fi

  local tmp api asset url tag
  tmp="$(mktemp_dir)"
  api="https://api.github.com/repos/ggml-org/llama.cpp/releases/latest"
  say "fetching the latest llama.cpp release"
  tag="$(curl -fsSL "$api" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$tag" ] || die "could not determine the latest llama.cpp release"
  case "$OS/$ARCH" in
    darwin/arm64) asset="llama-${tag}-bin-macos-arm64.tar.gz" ;;
    darwin/amd64) asset="llama-${tag}-bin-macos-x64.tar.gz" ;;
    linux/arm64)  asset="llama-${tag}-bin-ubuntu-arm64.tar.gz" ;;
    linux/amd64)  asset="llama-${tag}-bin-ubuntu-x64.tar.gz" ;;
    *) die "no llama.cpp binary for ${OS}/${ARCH}" ;;
  esac
  url="https://github.com/ggml-org/llama.cpp/releases/download/${tag}/${asset}"
  say "downloading ${asset}"
  if [ "$DRY_RUN" -eq 0 ]; then
    curl -fL --progress-bar "$url" -o "$tmp/$asset"
    mkdir -p "$DATA_DIR/llamacpp"
    tar -xzf "$tmp/$asset" -C "$DATA_DIR/llamacpp" || die "could not extract ${asset}"
    LLAMA_SERVER="$(find "$DATA_DIR/llamacpp" -type f -name llama-server | head -n1)"
    [ -n "$LLAMA_SERVER" ] || die "llama-server not found in the downloaded archive"
    chmod +x "$LLAMA_SERVER"
    ln -sfn "$LLAMA_SERVER" "$BIN_DIR/llama-server"
  fi
  ok "llama.cpp installed"
}

setup_llamacpp() {
  install_llamacpp
  LLAMA_MODEL_PATH=""

  if [ "$MODEL_EXPLICIT" -eq 1 ] && [ -n "$MODEL" ] && [ -f "$MODEL" ]; then
    LLAMA_MODEL_PATH="$MODEL"
    MODEL="$LLAMA_ALIAS"
    ok "using model file ${LLAMA_MODEL_PATH}"
  fi

  if [ -z "$LLAMA_MODEL_PATH" ]; then
    local existing
    if existing="$(find_gguf)"; then
      if [ "$MODEL_EXPLICIT" -eq 0 ]; then
        LLAMA_MODEL_PATH="$existing"
        ok "found existing model ${existing}"
      else
        LLAMA_MODEL_PATH="$existing"
      fi
    fi
  fi

  [ -n "$LLAMA_MODEL_PATH" ] || LLAMA_MODEL_PATH="$DATA_DIR/models/$GGUF_FILE"

  if [ ! -s "$LLAMA_MODEL_PATH" ] && [ "$PULL_MODEL" -eq 1 ]; then
    step "Downloading ${GGUF_FILE}"
    say "about 3 GB; saved to ${LLAMA_MODEL_PATH}"
    if [ "$DRY_RUN" -eq 0 ]; then
      mkdir -p "$(dirname "$LLAMA_MODEL_PATH")"
      curl -fL --progress-bar "$GGUF_URL" -o "$LLAMA_MODEL_PATH.part"
      mv "$LLAMA_MODEL_PATH.part" "$LLAMA_MODEL_PATH"
    fi
  elif [ -s "$LLAMA_MODEL_PATH" ]; then
    ok "model already present: ${LLAMA_MODEL_PATH}"
  elif [ "$PULL_MODEL" -eq 0 ]; then
    warn "no llama.cpp model found; the launcher will not start a model until one is configured"
  fi

  if [ "$MODEL_EXPLICIT" -eq 0 ] || [ -z "$MODEL" ]; then
    MODEL="$LLAMA_ALIAS"
  fi
  BACKEND=llamacpp
}

setup_lmstudio() {
  step "Configuring LM Studio"
  local lms
  if ! lms="$(find_lms)"; then
    die "LM Studio's CLI (lms) was not found. Install LM Studio from https://lmstudio.ai/download, open it once, then re-run with --advanced."
  fi
  ok "found lms: $lms"
  run "$lms" server start || warn "could not start the LM Studio server automatically"

  local model_key=""
  if [ "$MODEL_EXPLICIT" -eq 1 ] && [ -n "$MODEL" ]; then
    model_key="$MODEL"
    run "$lms" load "$model_key" -y --identifier "$model_key" || true
  else
    local -a models=()
    local m
    while IFS= read -r m; do [ -n "$m" ] && models+=("$m"); done < <(lmstudio_models "$lms")
    if [ "${#models[@]}" -gt 0 ]; then
      local choice idx
      choice="$(ask_choice "Which LM Studio model should Metis use?" 1 "${models[@]}")"
      idx=$((choice-1))
      [ "$idx" -ge 0 ] && [ "$idx" -lt "${#models[@]}" ] || idx=0
      model_key="${models[$idx]}"
      run "$lms" load "$model_key" -y --identifier "$model_key" || true
      ok "using existing model ${model_key}"
    elif [ "$PULL_MODEL" -eq 1 ]; then
      step "Importing ${GGUF_FILE} into LM Studio"
      local model_path="$DATA_DIR/models/$GGUF_FILE"
      if [ "$DRY_RUN" -eq 0 ]; then
        mkdir -p "$DATA_DIR/models"
        if [ ! -s "$model_path" ]; then
          curl -fL --progress-bar "$GGUF_URL" -o "$model_path.part"
          mv "$model_path.part" "$model_path"
        fi
        "$lms" import "$model_path" --user-repo metis/gemma-4-e2b --symbolic-link -y \
          || "$lms" import "$model_path" --user-repo metis/gemma-4-e2b --copy -y \
          || die "LM Studio could not import the model"
      fi
      model_key="$LLAMA_ALIAS"
      run "$lms" load "metis/gemma-4-e2b" -y --identifier "$model_key" || true
    else
      warn "no LM Studio model found; pass --model later or add one in LM Studio"
    fi
  fi
  BACKEND=lmstudio
  LMSTUDIO_MODEL="$model_key"
}

choose_backend() {
  local installed=() names=() lms_path=""
  have ollama && { installed+=(ollama); names+=("Ollama (installed)"); }
  if lms_path="$(find_lms)"; then
    installed+=(lmstudio); names+=("LM Studio (installed: ${lms_path})")
  fi
  have llama-server && { installed+=(llamacpp); names+=("llama.cpp (installed)"); }

  if [ "${#installed[@]}" -gt 0 ]; then
    local choice idx
    choice="$(ask_choice "Which backend should Metis use?" 1 "${names[@]}")"
    idx=$((choice-1))
    [ "$idx" -ge 0 ] && [ "$idx" -lt "${#installed[@]}" ] || idx=0
    BACKEND="${installed[$idx]}"
    return 0
  fi

  warn "no local backend detected"
  local choice
  choice="$(ask_choice "Nothing is installed yet. What should I install?" 1 \
    "Ollama + ${DEFAULT_MODEL} (recommended)" \
    "llama.cpp + ${DEFAULT_MODEL}" \
    "LM Studio (manual install)" \
    "Nothing, just install Metis")"
  case "$choice" in
    2) BACKEND=llamacpp ;;
    3) BACKEND=lmstudio ;;
    4) BACKEND=none ;;
    *) BACKEND=ollama ;;
  esac
}

# --- configuration ----------------------------------------------------------
write_config() {
  [ "$BACKEND" = "none" ] && return 0
  step "Writing configuration"
  if [ -f "$CONFIG_FILE" ] && [ "$DRY_RUN" -eq 0 ]; then
    local backup
    backup="${CONFIG_FILE}.bak.$(date +%Y%m%d%H%M%S)"
    cp "$CONFIG_FILE" "$backup"
    ok "backed up existing config to $backup"
  fi

  local provider="" base_url="" model=""
  case "$BACKEND" in
    ollama)   provider=ollama;     base_url="http://127.0.0.1:${OLLAMA_PORT}";    model="$MODEL" ;;
    lmstudio) provider=lmstudio;   base_url="http://127.0.0.1:${LMSTUDIO_PORT}";  model="${LMSTUDIO_MODEL:-}" ;;
    llamacpp) provider=compatible; base_url="http://127.0.0.1:${LLAMA_PORT}";     model="${MODEL:-$LLAMA_ALIAS}" ;;
    *) return 0 ;;
  esac

  if [ "$DRY_RUN" -eq 1 ]; then
    say "would write $CONFIG_FILE (provider=$provider model=${model:-auto})"
    return 0
  fi
  cat >"$CONFIG_FILE" <<EOF
{
  "provider": "${provider}",
  "base_url": "${base_url}",
  "model": "${model}",
  "temperature": 0.3,
  "max_steps": 40,
  "window": 64,
  "shell": true,
  "shell_timeout_sec": 120,
  "session": true,
  "highlight": "auto",
  "auto_approve": false
}
EOF
  chmod 0600 "$CONFIG_FILE"
  ok "wrote $CONFIG_FILE"
}

# --- launcher ---------------------------------------------------------------
# sed_repl escapes a value for use inside a sed s||| replacement.
sed_repl() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/&/\\&/g' -e 's/|/\\|/g'; }

write_wrapper() {
  step "Installing launcher"
  if [ "$DRY_RUN" -eq 0 ]; then
    cat >"$WRAPPER" <<'WRAPPER_EOF'
#!/usr/bin/env bash
# Generated by the Metis Agent installer. Starts the chosen backend on demand.
set -euo pipefail

DATA="@DATA@"
BACKEND="@BACKEND@"
MODEL="@MODEL@"
LLAMA_SERVER="@LLAMA_SERVER@"
LLAMA_MODEL="@LLAMA_MODEL@"
LLAMA_PORT="@LLAMA_PORT@"
OLLAMA_PORT="@OLLAMA_PORT@"
LMSTUDIO_PORT="@LMSTUDIO_PORT@"
LMS="@LMS@"
LOG="@LOG@"

# Do not start a backend for informational flags.
case "${1:-}" in
  -h|--help|-v|--version) exec "$DATA/bin/metis-agent" "$@" ;;
esac

healthy() { curl -fsS --max-time 2 "$1" >/dev/null 2>&1; }
wait_for() {
  local url="$1" i
  for ((i = 0; i < 120; i++)); do healthy "$url" && return 0; sleep 0.5; done
  return 1
}

case "$BACKEND" in
  ollama)
    if ! healthy "http://127.0.0.1:${OLLAMA_PORT}/api/tags"; then
      if command -v ollama >/dev/null 2>&1; then
        nohup ollama serve >>"$LOG" 2>&1 &
        wait_for "http://127.0.0.1:${OLLAMA_PORT}/api/tags" || true
      fi
    fi
    ;;
  lmstudio)
    if ! healthy "http://127.0.0.1:${LMSTUDIO_PORT}/v1/models"; then
      if [ -n "$LMS" ] && [ -x "$LMS" ]; then
        "$LMS" server start >>"$LOG" 2>&1 || true
        wait_for "http://127.0.0.1:${LMSTUDIO_PORT}/v1/models" || true
        if [ -n "$MODEL" ]; then
          "$LMS" load "$MODEL" -y --identifier "$MODEL" >>"$LOG" 2>&1 || true
        fi
      fi
    fi
    ;;
  llamacpp)
    if ! healthy "http://127.0.0.1:${LLAMA_PORT}/v1/models"; then
      if [ -x "$LLAMA_SERVER" ] && [ -s "$LLAMA_MODEL" ]; then
        nohup "$LLAMA_SERVER" -m "$LLAMA_MODEL" --alias "$MODEL" --jinja \
          --host 127.0.0.1 --port "$LLAMA_PORT" -c 8192 -ngl 99 \
          >>"$LOG" 2>&1 &
        wait_for "http://127.0.0.1:${LLAMA_PORT}/v1/models" || true
      fi
    fi
    ;;
esac

exec "$DATA/bin/metis-agent" "$@"
WRAPPER_EOF

    local lms_path="" llama_server="" llama_model=""
    [ "$BACKEND" = "lmstudio" ] && lms_path="$(find_lms || true)"
    [ "$BACKEND" = "llamacpp" ] && {
      llama_server="${LLAMA_SERVER:-$(command -v llama-server || true)}"
      llama_model="${LLAMA_MODEL_PATH:-$DATA_DIR/models/$GGUF_FILE}"
    }

    sed -i.bak \
      -e "s|@DATA@|$(sed_repl "$DATA_DIR")|g" \
      -e "s|@BACKEND@|$(sed_repl "$BACKEND")|g" \
      -e "s|@MODEL@|$(sed_repl "${MODEL:-}")|g" \
      -e "s|@LLAMA_SERVER@|$(sed_repl "$llama_server")|g" \
      -e "s|@LLAMA_MODEL@|$(sed_repl "$llama_model")|g" \
      -e "s|@LLAMA_PORT@|${LLAMA_PORT}|g" \
      -e "s|@OLLAMA_PORT@|${OLLAMA_PORT}|g" \
      -e "s|@LMSTUDIO_PORT@|${LMSTUDIO_PORT}|g" \
      -e "s|@LMS@|$(sed_repl "$lms_path")|g" \
      -e "s|@LOG@|$(sed_repl "$LOG_FILE")|g" \
      "$WRAPPER"
    rm -f "${WRAPPER}.bak"
    chmod 0755 "$WRAPPER"
  fi
  ok "launcher: $WRAPPER"

  # Also provide the capitalized spelling. On case-insensitive filesystems
  # (default macOS) this is the same file, so failure is expected and harmless.
  if [ "$DRY_RUN" -eq 0 ]; then
    if ln -sfn "$WRAPPER" "$BIN_DIR/Metis-Agent" 2>/dev/null; then
      ok "alias: $BIN_DIR/Metis-Agent"
    fi
  fi
}

ensure_path() {
  case ":${PATH}:" in
    *":${BIN_DIR}:"*) return 0 ;;
  esac
  step "Adding ${BIN_DIR} to PATH"
  local rc updated=0
  for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
    [ -f "$rc" ] || continue
    grep -q "metis-agent installer" "$rc" 2>/dev/null && { updated=1; continue; }
    if [ "$DRY_RUN" -eq 0 ]; then
      {
        printf '\n# Added by metis-agent installer\n'
        # shellcheck disable=SC2016 # write a literal $PATH, not this shell's
        printf 'export PATH="%s:$PATH"\n' "$BIN_DIR"
      } >>"$rc"
    fi
    if [ "$DRY_RUN" -eq 1 ]; then ok "would update $rc"; else ok "updated $rc"; fi
    updated=1
  done
  if [ "$updated" -eq 0 ]; then
    if [ "$DRY_RUN" -eq 0 ]; then
      {
        printf '\n# Added by metis-agent installer\n'
        # shellcheck disable=SC2016 # write a literal $PATH, not this shell's
        printf 'export PATH="%s:$PATH"\n' "$BIN_DIR"
      } >"$HOME/.profile"
    fi
    if [ "$DRY_RUN" -eq 1 ]; then ok "would create $HOME/.profile"; else ok "created $HOME/.profile"; fi
  fi

  # Fish does not read the POSIX rc files.
  local fish_dir="$HOME/.config/fish"
  if [ -d "$fish_dir" ]; then
    local fishrc="$fish_dir/config.fish"
    if ! grep -q "metis-agent installer" "$fishrc" 2>/dev/null; then
      if [ "$DRY_RUN" -eq 0 ]; then
        {
          printf '\n# Added by metis-agent installer\n'
          printf 'fish_add_path %s\n' "$BIN_DIR"
        } >>"$fishrc"
      fi
      if [ "$DRY_RUN" -eq 1 ]; then ok "would update $fishrc"; else ok "updated $fishrc"; fi
    fi
  fi

  warn "open a new terminal (or run: export PATH=\"${BIN_DIR}:\$PATH\")"
}

verify_install() {
  [ "$DRY_RUN" -eq 1 ] && return 0
  [ -x "$REAL_BIN" ] || die "binary missing at $REAL_BIN"
  "$REAL_BIN" --help >/dev/null 2>&1 || die "installed binary does not run; please report this"
  [ -x "$WRAPPER" ] || die "launcher missing at $WRAPPER"
  ok "metis-agent runs"
}

# --- main -------------------------------------------------------------------
if [ -z "$MODE" ]; then
  if [ "$TTY_FD" -eq 0 ]; then
    MODE=easy
  else
    printf '\n%sHow do you want to install Metis?%s\n' "$C_BOLD" "$C_RESET" >&"$TTY_FD"
    printf '  1) Easy     — Ollama + %s, everything configured (recommended)\n' "$DEFAULT_MODEL" >&"$TTY_FD"
    printf '  2) Advanced — choose Ollama, llama.cpp, or LM Studio\n' >&"$TTY_FD"
    printf 'Choice [1]: ' >&"$TTY_FD"
    IFS= read -r mode_choice <&"$TTY_FD" || mode_choice=""
    case "${mode_choice:-1}" in 2) MODE=advanced ;; *) MODE=easy ;; esac
  fi
fi

if [ "$MODE" = "advanced" ]; then
  step "Advanced setup"
  [ -n "$BACKEND" ] || choose_backend
else
  MODE=easy
  [ -n "$BACKEND" ] || BACKEND=ollama
  [ "$BACKEND" = "none" ] && INSTALL_BACKEND=0
fi

[ "$BACKEND" = "none" ] && INSTALL_BACKEND=0

install_metis

if [ "$INSTALL_BACKEND" -eq 1 ]; then
  if [ "$DRY_RUN" -eq 1 ]; then
    step "Would configure backend: $BACKEND"
    case "$BACKEND" in
      ollama)
        say "would install/start Ollama and use ${MODEL:-$DEFAULT_MODEL}"
        [ -n "$MODEL" ] || MODEL="$DEFAULT_MODEL"
        ;;
      llamacpp)
        say "would install llama.cpp and use ${MODEL:-$LLAMA_ALIAS}"
        [ -n "$MODEL" ] || MODEL="$LLAMA_ALIAS"
        LLAMA_MODEL_PATH="$DATA_DIR/models/$GGUF_FILE"
        ;;
      lmstudio)
        say "would start LM Studio and select/import a model"
        ;;
    esac
  else
    case "$BACKEND" in
      ollama) setup_ollama ;;
      llamacpp) setup_llamacpp ;;
      lmstudio) setup_lmstudio ;;
    esac
  fi
fi

write_config
write_wrapper
ensure_path
verify_install

step "Done"
if [ "$BACKEND" = "none" ]; then
  say "Metis is installed. Configure a backend later with: metis-agent --provider ... --model ..."
else
  say "Backend: $BACKEND"
  [ -n "${MODEL:-}" ] && say "Model:   $MODEL"
fi
say ""
say "Run it from anywhere, including your home folder:"
say "  ${C_BOLD}metis-agent${C_RESET}    (or ${C_BOLD}Metis-Agent${C_RESET})"
say ""
say "The launcher starts the backend automatically if it is not running."
say "Logs: $LOG_FILE"
