# Security policy

To report a vulnerability, use GitHub's private vulnerability reporting: on the repository, open the Security tab and choose "Report a vulnerability". Please do not open a public issue for it.

In scope: the `jalon` binary, `jalon init` and the edits it makes to your settings, and the release archives, their checksums and attestations, and the Homebrew cask in `a-lafon/homebrew-tap`.

Jalon makes no network call, and reads and writes only the files listed in the [README](README.md#what-jalon-touches).
