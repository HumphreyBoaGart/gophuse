# Gophuse

Mounts a gopher:// server as a read-only filesystem in Linux using FUSE.


## Install

Make sure you have `fuse3` installed. Then download the repo and stash the bin somewhere:

### For Yourself

Where {ARCH} is, you have three install options:

1) **amd64**
2) **arm64** *(untested)*
3) **riscv64** *(untested)*

```
git clone https://github.com/humphreyboagart/gophuse
cp gophuse/bin/gophuse-linux-{ARCH} ~/.local/bin/gophuse
chmod 700 ~/.local/bin/gophuse
```

### For All Users

```
git clone https://github.com/humphreyboagart/gophuse
cp gophuse/bin/gophuse-linux-{ARCH} /usr/local/bin/gophuse
chmod 755 /usr/local/bin/gophuse
```
