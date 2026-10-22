#!/bin/sh
set -eu

python_bin=$(command -v python3)
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
exec "$python_bin" -I -B "$script_dir/public_scan.py" "$@"
