# gophuse Usage

`gophuse` mounts a Gopher server as a read-only FUSE filesystem.

## Runtime requirements

- Linux with FUSE support
- `fusermount3` from the Debian `fuse3` package
- `/dev/fuse` available to the user running `gophuse`

`gophuse` stores local metadata under:

`~/.local/share/gophuse`

Current layout:

- `~/.local/share/gophuse/mounts/` for active mount metadata
- `~/.local/share/gophuse/cache/` reserved for future on-disk cache data

## Commands

Mount a server:

```bash
gophuse mount bestpoint.institute
```

That automatically mounts to:

```bash
~/mnt/bestpoint.institute
```

Mount a server to an explicit path:

```bash
gophuse mount bestpoint.institute ~/mnt/bestpoint
```

Specify a non-default port explicitly:

```bash
gophuse mount bestpoint.institute:7070
```

Full `gopher://` URLs still work too:

```bash
gophuse mount gopher://bestpoint.institute:70
```

Mount in the foreground:

```bash
gophuse mount --foreground bestpoint.institute
```

Show active `gophuse` mounts:

```bash
gophuse list
```

Show the live menu for a Gopher URL:

```bash
gophuse cat bestpoint.institute
```

Unmount a mounted server:

```bash
gophuse unmount bestpoint.institute
```

Or unmount by explicit path:

```bash
gophuse unmount ~/mnt/bestpoint
```

`unmount` removes the mountpoint directory if it is empty, and also removes any empty parent directories that `gophuse` created for that mount.
