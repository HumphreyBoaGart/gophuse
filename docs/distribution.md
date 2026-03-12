# Distribution Notes

## Binary portability

The release binaries are built as statically linked Linux executables:

- `bin/gophuse-amd64`
- `bin/gophuse-arm64`
- `bin/gophuse-riscv64`

That means:

- it does not require Go on the target machine
- it does not depend on the target system's glibc or other shared libraries
- it should run on other modern Linux distributions of the same CPU architecture

The current build target is:

- Linux
- `x86_64` / `amd64`

So the current binary is expected to work on:

- Debian 12/13 and related distributions
- Fedora on `x86_64`
- Arch Linux on `x86_64`
- other modern `x86_64` Linux systems with working FUSE support

Current release status:

- the `amd64` binary has been runtime-tested
- the `arm64` binary has been cross-compiled but not yet runtime-tested on a real machine
- the `riscv64` binary has been cross-compiled but not yet runtime-tested on a real machine

It is not expected to work unchanged on:

- non-Linux systems
- ARM systems
- 32-bit systems

## Runtime requirements

Even though the binary itself is self-contained, mounting still requires FUSE support from the target system:

- Linux kernel FUSE support
- `/dev/fuse`
- `fusermount3` or `fusermount`
- permission for the user to perform FUSE mounts

Typical package names:

- Debian/Ubuntu: `fuse3`
- Fedora: `fuse3`
- Arch: `fuse3`

## Practical support statement

Recommended wording for release notes:

`gophuse` is distributed as a statically linked `x86_64` Linux binary. It should run on modern Linux distributions such as Debian, Fedora, and Arch, provided the target system has working FUSE support (`/dev/fuse` and `fusermount3` or `fusermount`).

## Main portability risks

- missing FUSE userspace tools on the target machine
- local policy preventing unprivileged FUSE mounts
- different CPU architecture than the distributed binary
- unusual kernel or container environments where `/dev/fuse` is unavailable
