# Nemo Integration

`gophuse` can be launched from Nemo with a lightweight per-user action that
opens a dialog, prompts for a Gopher address, mounts it, and opens the mounted
directory in Nemo.

This integration uses:

- a `.nemo_action` file in `~/.local/share/nemo/actions/`
- a small helper script in `~/.local/share/gophuse/nemo/`
- `zenity` for the address prompt and error dialogs

## Install

Requirements:

- `nemo`
- `zenity`
- `gophuse` available in `PATH`

Install the action:

```bash
./scripts/install-nemo-action.sh
```

Then restart Nemo:

```bash
nemo -q
```

Open Nemo again and use:

`Mount Gopher Server...`

## Accepted input

The dialog accepts the same target forms as the CLI:

- `bestpoint.institute`
- `bestpoint.institute:7070`
- `192.0.2.10`
- `gopher://bestpoint.institute`

If no port is specified, `gophuse` defaults to port `70`.
