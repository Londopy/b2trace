# Changelog

All notable changes to b2trace are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). The section for a version is
what appears on its GitHub release page.

## [Unreleased]

## [0.2.0] - 2026-11-19

### Added

- Packet: `b2trace proxy --modem agwpe` records sessions through an AGWPE TNC such as Direwolf,
  with the frames waiting for an acknowledgement in place of BUFFER reports.
- Every command-port line but the BUFFER reports (on AGWPE, every frame on Pat's connection
  other than data and frame counts) is kept in the session's `commands`, with up to 50 lines
  from before CONNECTED.
- `connect_ms`: time from Pat's connect command to CONNECTED.
- `sid_out` and `sid_in`, and a `proposals` list with each proposal's code, sizes, optimistic
  flag and the answer it got, including resume offsets.
- `--link` puts a label such as `varahf` or `varafm` on each session.
- `b2trace summary` estimates the time spent on each phase, shows the SIDs and the proposals
  and answers each way, and takes several log files.
- `b2trace share` strips callsigns, locators, addresses, titles and MIDs from a log, and
  `b2trace results` turns shared logs into the table on the new results page,
  [RESULTS.md](https://github.com/Londopy/b2trace/blob/main/RESULTS.md).

## [0.1.0] - 2026-11-19

### Added

- `b2trace proxy`: a passive TCP proxy between Pat and a VARA or ARDOP modem that writes one
  JSON line per session, with bytes per B2F phase and direction, turns, local PTT intervals,
  BUFFER reports, messages and errors.
- `b2trace summary`: a plain-text breakdown of each recorded session and of all of them.
- `b2trace compress`: compares LZHUF, gzip, brotli and zstd, with and without a
  cross-validated dictionary, on Winlink message files.
- `b2trace version`.

[Unreleased]: https://github.com/Londopy/b2trace/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/Londopy/b2trace/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Londopy/b2trace/releases/tag/v0.1.0
