#!/bin/sh
set -eu
cd -- "$(dirname -- "$0")"
exec ./ki-basis agent-ui --open-browser
