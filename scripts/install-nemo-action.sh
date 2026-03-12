#!/bin/sh
set -eu

# Install the per-user Nemo action and helper script. This keeps the integration
# outside the repository tree so Nemo can discover it in the normal XDG path.

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
ACTION_SRC="$ROOT_DIR/nemo/gophuse-mount.nemo_action"
HELPER_SRC="$ROOT_DIR/scripts/gophuse-nemo-mount.sh"

ACTION_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/nemo/actions"
HELPER_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/gophuse/nemo"

mkdir -p "$ACTION_DIR" "$HELPER_DIR"
install -m 0644 "$ACTION_SRC" "$ACTION_DIR/gophuse-mount.nemo_action"
install -m 0755 "$HELPER_SRC" "$HELPER_DIR/gophuse-nemo-mount.sh"

printf '%s\n' "Installed Nemo action to $ACTION_DIR/gophuse-mount.nemo_action"
printf '%s\n' "Installed helper to $HELPER_DIR/gophuse-nemo-mount.sh"
printf '%s\n' "Restart Nemo to pick up the new action."

