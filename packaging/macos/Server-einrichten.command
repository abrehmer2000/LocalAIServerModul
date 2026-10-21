#!/bin/bash
set -euo pipefail
cd -- "$(dirname -- "$0")"
exec ./ki-basis agent-ui --dir "$PWD/Manager-Daten" --open-browser
