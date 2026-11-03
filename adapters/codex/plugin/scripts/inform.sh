#!/bin/sh
# A missing helper must preserve the owner's ordinary prompt.
run() {
  if [ ! -L "$1" ] && [ ! -L "$1/peerturn" ] && [ -f "$1/peerturn" ] && [ -x "$1/peerturn" ]; then
    exec "$1/peerturn" codex-hook
  fi
}
case "$PLUGIN_ROOT" in
  /*)
    run "$PLUGIN_ROOT/bin"
    # A plugin added from the repository bundles no executable. It may use only
    # the release helper the signed installer verified, never one from PATH.
    if [ ! -e "$PLUGIN_ROOT/bin" ] && [ ! -L "$PLUGIN_ROOT/bin" ]; then
      case "$HOME" in
        /*) run "$HOME/.local/share/peerturn/0.1.6-preview" ;;
      esac
    fi
    ;;
esac
printf '{}\n'
