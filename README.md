# Gophuse

Mounts a gopher:// server as a read-only filesystem in Linux using FUSE.

This tool was made for the [article on Gopher](https://bestpoint.institute/tools/gopher) at my wiki, just to show how easy it can be done. There used to be tools that did this many years ago, but they're all abandonware. Better to just rebuild the function as a modern Go binary with ChatGPT.


## Install

Make sure you have `fuse3` installed. Then download the repo and stash the bin somewhere.

Where {ARCH} is, you have three install options:

1) **amd64**
2) **arm64** *(untested)*
3) **riscv64** *(untested)*

### For Yourself

```
git clone https://github.com/humphreyboagart/gophuse
cp gophuse/bin/gophuse-{ARCH} ~/.local/bin/gophuse
chmod 700 ~/.local/bin/gophuse
```

### For All Users

```
git clone https://github.com/humphreyboagart/gophuse
cp gophuse/bin/gophuse-{ARCH} /usr/local/bin/gophuse
chmod 755 /usr/local/bin/gophuse
```
