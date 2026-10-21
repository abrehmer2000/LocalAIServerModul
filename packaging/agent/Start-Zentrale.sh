#!/bin/sh
set -eu
cd -- "$(dirname -- "$0")"
exec ./ki-basis quickstart --dir "${XDG_CONFIG_HOME:-$HOME/.config}/KI-Basis/Zentrale" --open-browser
