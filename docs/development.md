# gophuse Development

## Repository contents

Files that should usually go to GitHub:

- `cmd/gophuse/main.go`
- `go.mod`
- `go.sum`
- `bin/` Prebuilt binaries
- `docs/` End-user documentation

Files that should not usually go to GitHub:

- `.cache/` Go build and module cache used in this workspace
- `.gopath/` local GOPATH data used for isolated builds
- temporary mount test directories such as `mnt/` and `.mnt-*`

## Build release binaries

```bash
./scripts/build-release.sh
```

This generates static Linux binaries for:

- `amd64`
- `arm64`
- `riscv64`

## Notes

- The binary is built as a static executable.
- `gophuse-*` uses `go-fuse` and does not require Go on the target machine.
- The target machine still needs FUSE runtime support to perform mounts.
