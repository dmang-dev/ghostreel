# Security

## Reporting a vulnerability

Please report security problems privately, not in a public issue: use **Report a vulnerability** on the repository's **Security** tab (GitHub's private vulnerability reporting). Say what's affected, how to reproduce it, and what an attacker could do. Please give the maintainers time to fix it before you make it public.

Only the latest release is supported.

## What's in scope

ghostreel reads files it didn't make, so these matter most:

- **Replays and maps.** Both are untrusted input: a crafted `.SC2Replay`, `.SC2Map` or `.s2ma` must not be able to crash ghostreel in a way that runs code, read files outside what it's given, or hang it indefinitely.
- **The self-updater** (desktop app). It installs only a release whose `latest.json` is signed with the release key and whose files match that manifest's SHA-256 (`internal/update`, `desktop/update.go`). A way around either check is critical.
- **The local server** (`ghostreel serve`, and the desktop app's API). It listens on localhost by default; anything that lets a web page or another machine open files, or reach the desktop-only endpoints (opening replays by path, settings, updates), is in scope.
- **Release builds.** The release workflow and anything that could change what it publishes.

The game's own data, read from the user's install, is trusted as far as it is the user's own install.
