# Installing Jalon

Every way ends with `jalon init`, which adds the hooks and the status line to `~/.claude/settings.json`; `jalon init --dry-run` shows what it would change first.

## Homebrew

```sh
brew install a-lafon/tap/jalon
jalon init
```

The cask clears the macOS quarantine mark on the binary it installs, and never runs `jalon init` for you.

## Go

With Go 1.26 or later, `go install github.com/a-lafon/jalon/cmd/jalon@latest` builds it into `$(go env GOPATH)/bin`, which must be on your `PATH`.

## An archive

Each [release](https://github.com/a-lafon/jalon/releases) has one archive per platform, `jalon_<version>_<os>_<arch>.tar.gz`, with `<os>` `darwin` or `linux` and `<arch>` `amd64` or `arm64`, and a `checksums.txt`. An archive holds `jalon`, the README and the LICENSE.

```sh
tar xzf jalon_0.1.0_linux_amd64.tar.gz jalon   # the archive you downloaded
mv jalon ~/.local/bin/                         # or /usr/local/bin, any directory on your PATH
jalon version                                  # jalon 0.1.0 (<commit>, <build date>)
jalon init                                     # adds the hooks and the status line to ~/.claude/settings.json
```

## Check the checksum

With the archive and `checksums.txt` in the same folder:

```sh
shasum -a 256 -c --ignore-missing checksums.txt
```

It prints `OK` for the archive you downloaded. This catches a corrupted download.

## Check where it was built

Each archive and `checksums.txt` has a build provenance attestation. With the [GitHub CLI](https://cli.github.com):

```sh
gh attestation verify jalon_0.1.0_linux_amd64.tar.gz --repo a-lafon/jalon
```

A successful check shows that the file was built by this repository's release workflow, from the commit the tag names. It does not tell you the code is safe, and it is not a signature that macOS recognises: the binaries are not signed or notarized.

## macOS and the quarantine

macOS marks a file downloaded through a browser as quarantined, and refuses to run an unsigned binary with that mark ("cannot verify the developer"). Clear it once, on the extracted binary:

```sh
xattr -d com.apple.quarantine ~/.local/bin/jalon
```

A file fetched with `curl` or `gh release download` is not marked.

## Update

- Homebrew: `brew upgrade jalon`.
- Go: run the `go install` line again.
- An archive: download the new one and replace `jalon` at the same path.

`jalon init` recorded that path, so the hooks keep working without a new `jalon init`, except after an upgrade from a version without the stop hook (0.1.0): run `jalon init` once more to add it, with `--mod` when you use the mod, since plain `jalon init` adds the status line; `jalon version` shows the version you now run. To remove Jalon, run `jalon init --uninstall` before `brew uninstall jalon` or deleting the binary.
