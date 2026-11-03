#!/bin/sh
# Until setup configures the verified executable, keep the owner's prompt
# ordinary instead of failing every prompt with an unset plugin option.
binary=${CLAUDE_PLUGIN_OPTION_PEERTURN_BINARY-}
case "$binary" in
  /*)
    if [ ! -L "$binary" ] && [ -f "$binary" ] && [ -x "$binary" ]; then
      exec "$binary" adapter claude owner-boundary
    fi
    ;;
esac
cat >/dev/null
printf '{}\n'
