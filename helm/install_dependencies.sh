#!/usr/bin/env bash
cd relayforge/
command -v uv >/dev/null || curl -LsSf https://astral.sh/uv/install.sh | sh
source "$HOME/.local/bin/env" 2>/dev/null || true
uv lock && uv sync