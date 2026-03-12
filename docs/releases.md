# Release Builds

## Multi-architecture static builds

Use the release build script to generate static Linux binaries for the supported architectures:

```bash
./scripts/build-release.sh
```

Default outputs:

- `bin/gophuse-amd64`
- `bin/gophuse-arm64`
- `bin/gophuse-riscv64`
- `bin/SHA256SUMS`

The script builds with:

- `CGO_ENABLED=0`
- `-trimpath`
- `-ldflags='-s -w'`

and keeps build caches local to the repository workspace:

- `.cache/go-build`
- `.cache/go-mod`
- `.gopath`

## Custom targets

You can override the target list with `TARGETS`:

```bash
TARGETS="linux/amd64 linux/arm64" ./scripts/build-release.sh
```

## Verification

Check an artifact with:

```bash
file bin/gophuse-arm64
```

On the matching target system, also verify:

```bash
ldd bin/gophuse-arm64
```

Expected result for a static binary:

- `statically linked` from `file`
- `not a dynamic executable` from `ldd`

Check release checksums with:

```bash
sha256sum -c bin/SHA256SUMS
```

## GitHub release assets

Recommended release assets:

- `gophuse-amd64`
- `gophuse-arm64`
- `gophuse-riscv64`
- `SHA256SUMS`

Current testing status:

- `gophuse-amd64` has been build-tested and runtime-tested
- `gophuse-arm64` has been build-tested only and is still untested on real hardware
- `gophuse-riscv64` has been build-tested only and is still untested on real hardware

Recommended release note text:

`gophuse` is distributed here as statically linked Linux binaries for `amd64`, `arm64`, and `riscv64`. The target system still needs working FUSE support, including `/dev/fuse` and `fusermount3` or `fusermount`.

## Notes

- Cross-compilation is handled by Go and does not require a cross C toolchain here.
- The binaries still require working FUSE support on the target Linux system.
