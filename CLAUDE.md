# CLAUDE.md

See [AGENTS.md](AGENTS.md) for how this project is built, tested and packaged.

## Exceptions to the global rules

- **Signing key in CI secrets.** The user approved (3 Oct 2026) exporting the shared
  Launchpad signing key, `2A128435A6FE8BD751AA578720959AB807096ADB` ("Will Rouesnel (GPG
  key for launchpad signing)"), into this repository's GitHub secrets
  `PACKAGE_SIGNING_KEY` and `PACKAGE_SIGNING_KEY_PASSPHRASE`, with its fingerprint in the
  `PACKAGE_SIGNING_KEY_FINGERPRINT` variable, so the release workflow can sign PPA
  uploads. Its passphrase is looked up only with
  `secret-tool lookup service gpg-passphrase fingerprint <fpr>`, and never printed.
- The project's earlier key, `4EE96BF5C3BE937FDD2D010947CCC9AA4B3FD5DE`, is no longer used.
  It stays in the personal keyring: revoking or deleting it needs the user's OK.
