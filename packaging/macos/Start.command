#!/bin/bash
set -euo pipefail
cd -- "$(dirname -- "$0")"
if [[ "$(uname -s)" != "Darwin" || "$(uname -m)" != "arm64" ]]; then
  echo "Dieses Paket ist fuer Macs mit Apple Silicon (M1 und neuer)."
  read -r -p "Enter zum Schliessen. " taskClose
  exit 1
fi
./ki-basis quickstart --dir "$PWD/Daten" --open-browser
read -r -p "Enter zum Schliessen. " taskClose
