#!/bin/sh
set -eu

# Carry each linked module's own notices into the shell-free runtime image.
module_cache=$(go env GOMODCACHE)
go list -m -f '{{.Dir}}' all | while IFS= read -r module_dir; do
  case "$module_dir" in
    "$module_cache"/*)
      relative_dir=${module_dir#"$module_cache"/}
      destination="/licenses/third-party/$relative_dir"
      mkdir -p "$destination"
      for notice in "$module_dir"/LICENSE* "$module_dir"/COPYING* "$module_dir"/NOTICE*; do
        if [ -f "$notice" ]; then cp "$notice" "$destination/"; fi
      done
      ;;
  esac
done
