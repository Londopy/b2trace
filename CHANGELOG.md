# Changelog

All notable changes to b2trace are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). The section for a version is
what appears on its GitHub release page.

## [Unreleased]

### Added

- `b2trace proxy`: a passive TCP proxy between Pat and a VARA or ARDOP modem that writes one
  JSON line per session, with bytes per B2F phase and direction, turns, local PTT intervals,
  BUFFER reports, messages and errors.
- `b2trace summary`: a plain-text breakdown of each recorded session and of all of them.
- `b2trace compress`: compares LZHUF, gzip, brotli and zstd, with and without a
  cross-validated dictionary, on Winlink message files.
- `b2trace version`.

[Unreleased]: https://github.com/Londopy/b2trace/commits/main
