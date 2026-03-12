#!/bin/sh
set -eu

# This helper is launched by Nemo via a .nemo_action entry. It asks the user
# for a Gopher host or URL, mounts it with gophuse, and opens the resulting
# mountpoint in Nemo.

if ! command -v zenity >/dev/null 2>&1; then
  printf '%s\n' "gophuse Nemo helper requires zenity" >&2
  exit 1
fi

if ! command -v gophuse >/dev/null 2>&1; then
  zenity --error \
    --title="gophuse" \
    --text="The gophuse command was not found in PATH."
  exit 1
fi

target="$(
  zenity --entry \
    --title="Mount Gopher Server" \
    --text="Enter a Gopher host, host:port, IP, or gopher:// URL:" \
    --entry-text="bestpoint.institute"
)" || exit 0

target="$(printf '%s' "$target" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
if [ -z "$target" ]; then
  exit 0
fi

if ! mountpoint="$(gophuse mount "$target" 2>&1)"; then
  zenity --error \
    --title="gophuse mount failed" \
    --width=480 \
    --text="$mountpoint"
  exit 1
fi

zenity --notification \
  --text="Mounted $target at $mountpoint" \
  >/dev/null 2>&1 || true

if command -v nemo >/dev/null 2>&1; then
  nemo "$mountpoint" >/dev/null 2>&1 &
elif command -v xdg-open >/dev/null 2>&1; then
  xdg-open "$mountpoint" >/dev/null 2>&1 &
fi

