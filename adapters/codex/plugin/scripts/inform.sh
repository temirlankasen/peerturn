#!/bin/sh
# A missing helper must preserve the owner's ordinary prompt.
case "$PLUGIN_ROOT" in
  /*)
    if [ ! -L "$PLUGIN_ROOT/bin" ] && [ ! -L "$PLUGIN_ROOT/bin/peerturn" ] &&
       [ -f "$PLUGIN_ROOT/bin/peerturn" ] && [ -x "$PLUGIN_ROOT/bin/peerturn" ]; then
      exec "$PLUGIN_ROOT/bin/peerturn" codex-hook
    fi
    ;;
esac
printf '{}\n'
