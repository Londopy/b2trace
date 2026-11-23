# Changelog

All notable changes to b2trace are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). The section for a version is
what appears on its GitHub release page.

## [Unreleased]

### Added

- A terminal UI. `b2trace proxy --ui` shows the session in progress: connection, PTT, the
  current B2F phase and direction, turns, the byte and time tables, and the proxy's log.
  `b2trace ui` browses a session log with the full summary of each session, writes a shared
  copy, and runs compress. It follows the terminal's light or dark background.
- Tests that every corpus message is exactly what wl2k-go writes for it, and that the totals
  in COMPRESSION.md are what `b2trace compress` gives on the corpus.
- The README says what has and has not been run against real modems and TNCs.

### Changed

- `b2trace compress` uses one zstd encoder instead of one per CPU core. The results are the
  same, and on a 24-core machine it needs about a fifth of the memory.

## [0.3.0] - 2026-11-19

### Added

- The reply time, in `b2trace summary` and the results table: how long Pat took to answer
  each remote turn, from the remote's last byte to Pat's next one.
- A corpus of 74 Winlink messages written for this repository, the script that makes it, and
  [COMPRESSION.md](https://github.com/Londopy/b2trace/blob/main/COMPRESSION.md) with how LZHUF,
  gzip, brotli and zstd with a trained dictionary compare on it.

### Changed

- `b2trace compress` prints the size of the dictionaries it trained, which can be well under
  `--dict-size`.

### Fixed

- The README read as if b2trace knew what the proposal codes mean. It now says where FC and FD
  come from and that FE is only a proposal.
- The test data uses made-up callsigns, ones that are never issued.
- The 0.2.0 notes read as if packet had been tested with Direwolf. The README now says how far
  it has been: real Direwolf for registering and dialing, a stand-in TNC for full sessions.

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

[Unreleased]: https://github.com/Londopy/b2trace/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/Londopy/b2trace/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/Londopy/b2trace/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Londopy/b2trace/releases/tag/v0.1.0
