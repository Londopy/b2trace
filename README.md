# b2trace

[![CI](https://github.com/Londopy/b2trace/actions/workflows/ci.yml/badge.svg)](https://github.com/Londopy/b2trace/actions/workflows/ci.yml)
[![changelog](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/Londopy/b2trace/gh-pages/changelog-badge.json)](CHANGELOG.md)
[![License](https://img.shields.io/github/license/Londopy/b2trace)](LICENSE)

**See where the airtime goes in a Winlink B2F session.**

b2trace sits between Pat (or any Winlink client) and a VARA or ARDOP sound-card modem as a
passive TCP proxy. It forwards every byte unchanged, follows the B2F protocol in both
directions, and writes one JSON line per session: how many bytes went to the handshake, the
proposals, the messages and the answers, how often the link turned around, and how long your
own transmitter was keyed. A second command compares compressors on Winlink messages.

A session in `b2trace summary` looks like this (numbers from the test data, not a real link):

```
session 1: vara, 2026-11-19 18:02:11, 3m10.4s, local N0CALL, remote LA1B, ended: disconnected

bytes              out        in      both   share
handshake          100       150       250   11.4%
proposals           60        50       110    5.0%
payload           1800         0      1800   81.8%
answers/end          4         9        13    0.6%
other                0        27        27    1.2%
total             1964       236      2200  100.0%

turns             6
turnarounds       5
PTT intervals     3
local TX          12.3 s
gaps              2
mean gap          2.50 s (estimate: local PTT off to next PTT on)
possible retries  2 (BUFFER non-zero and unchanged for 2+ samples)
messages          1 out, 0 in
errors            0
```

## Install

Download the archive for your system from [Releases](https://github.com/Londopy/b2trace/releases):
Windows (64-bit or ARM), Linux (64-bit, or ARM for a Raspberry Pi with a 32- or 64-bit OS)
and macOS. Each holds the one `b2trace` program; unpack it and run it from a terminal. The
binaries are not code-signed, so check them against `SHA256SUMS.txt` on the release page.

Or, with Go 1.26 or newer:

```
go install github.com/Londopy/b2trace@latest
```

## Usage

### Record sessions

The proxy takes the modem's usual ports, so Pat needs no change, and passes everything on to
the modem on ports you move it to. Both modems use one port for commands and the next one for
data, so every address below stands for a pair.

**VARA**

1. In VARA's settings, move its TCP ports from 8300 and 8301 to 18300 (command) and 18301 (data).
2. Start the proxy:
   ```
   b2trace proxy --modem vara --target 127.0.0.1:18300
   ```
3. Use Pat as usual. Its `varahf` or `varafm` address stays `localhost:8300`, which is now the
   proxy.

**ARDOP**

1. Start the ARDOP TNC on port 18515 instead of 8515 (ardopc and ardopcf take the port as their
   first argument).
2. Start the proxy:
   ```
   b2trace proxy --modem ardop --target 127.0.0.1:18515
   ```
3. Pat's `ardop` address stays `localhost:8515`.

If Pat is set to another address, pass that address to `--listen`. With ARDOP, Pat finds the
data port by adding one to the last digit of the address, so pick a listen port that does not
end in 9.

The proxy prints every command-port line with a timestamp. It appends a session to
`sessions.jsonl` (change it with `--log`) two seconds after the modem reports DISCONNECTED, when
a connection to the modem closes, or when you stop the proxy with Ctrl+C. If the modem is not
running, the proxy closes Pat's connection and prints the address it could not reach.

### Read the results

```
b2trace summary sessions.jsonl
```

For each session and for all of them together: bytes per phase in each direction and their
share of all bytes, turns and turnarounds, local transmit time, the mean gap between your
transmissions, and possible retries (times the modem's BUFFER report stayed the same and
non-zero over two or more reports).

The phases, as b2trace sorts each line or transfer:

| Phase | What is in it |
|---|---|
| handshake | the SID (`[...]`), `;FW`, `;PQ`, `;PR`, other `;` lines, MOTD and prompt lines, before that side's first F-command |
| pending | `;PM` pending-message lines (shown with proposals in the summary) |
| proposals | `FA` to `FE` proposals and the `F>` line that ends a block |
| answer | `FS` |
| payload | binary message transfers, header and block framing included |
| end | `FF` and `FQ` |
| error | lines starting with `***` |
| other | anything else after that side's first F-command |

### Compare compressors

```
# Pat's mailbox on Windows (PowerShell)
b2trace compress "$env:LOCALAPPDATA\pat\mailbox\N0CALL"
# on Linux, as CSV
b2trace compress --csv ~/.local/share/pat/mailbox/N0CALL > sizes.csv
```

Give it files or directories (searched recursively): Pat's `.b2f` messages or any text files.
For each file it prints the raw size and the size after:

- `lzhuf`: B2F's own compression, made with wl2k-go, including the 6-byte header
- `gzip`: level 9
- `brotli`: quality 11, 4 MB window
- `zstd`: level 19
- `zstd+dict`: level 19 with a dictionary of up to `--dict-size` bytes (default 16384)

Sizes include each format's own header and checksum. The footer has the total bytes per column
and, per column, the median over files of the size relative to LZHUF. zstd comes from
klauspost/compress, which maps level 19 to its best level; its authors put that near zstd
level 11, so the reference zstd at `-19` may do a little better.

For `zstd+dict`, the sorted files are split into two halves by odd and even position. Each half
is compressed with a dictionary trained on the other half, so no file helps compress itself.
Files with the same content as another file are left out for the same reason. With too few
files to train a dictionary, the column is skipped and the reason printed. The dictionary
builder is not deterministic, so this column can change by a fraction of a percent between
runs; the other columns always come out the same.

## What one side can and cannot measure

b2trace sees only your end of the link.

- **Exact:** your own transmit time, from the PTT messages the modem sends on its command port
  (`PTT ON`/`PTT OFF` from VARA, `PTT TRUE`/`PTT FALSE` from ARDOP). If your setup sends none,
  `local_tx_ms` stays 0 and there are no gaps.
- **Exact:** B2F bytes per phase and direction, turns, message sizes, and the BUFFER reports as
  the modem sent them.
- **Estimated:** the remote station's transmit time and the turnaround dead time. b2trace never
  sees the remote's PTT. All it has is `gap_ms`, the time from your PTT going off to it coming
  on again: the remote's transmission plus the dead time on both sides of it. One station
  cannot split those.
- **Not airtime:** turn times (`first_byte`, `last_byte`) are when the bytes passed the proxy.
  Outgoing bytes can wait in the modem's buffer before they go on the air, and incoming bytes
  show up only after the modem has decoded them.
- **Not airtime:** byte shares. VARA compresses data itself, both modems add ARQ framing and
  acknowledgements, and their speed changes with conditions, so a phase's share of the bytes
  is not its share of the seconds.
- **A hint, not a count:** possible retries. A BUFFER report that repeats a non-zero value means
  nothing was acknowledged in between, which is often a repeated frame, but the modem does not
  say so.

## JSON fields

One line per session. Times are RFC 3339 with milliseconds, in the time zone of the machine
running the proxy. `out` is what your station sent, `in` is what it received.

| Field | Meaning |
|---|---|
| `modem` | `vara` or `ardop` |
| `start`, `end` | when the proxy saw CONNECTED and DISCONNECTED, or when the session was cut short |
| `end_reason` | `disconnected`, `data connection closed`, `command connection closed`, `new CONNECTED` or `proxy stopped` |
| `connect_line` | the modem's CONNECTED line as sent, with the bandwidth |
| `local_call`, `remote_call` | from MYCALL and the CONNECTED line; left out when unknown |
| `bytes_out`, `bytes_in` | B2F bytes per phase: `handshake`, `pending`, `proposals`, `answer`, `payload`, `end`, `error`, `other` |
| `turn_count`, `turnarounds` | runs of data in one direction, and the changes of direction between them |
| `turns` | each turn: `dir`, `first_byte`, `last_byte` and `bytes` per phase |
| `local_tx_ms` | total time your PTT was on (exact) |
| `ptt` | each PTT interval: `on`, `off`, `local_tx_ms` |
| `gap_ms` | each time from a PTT OFF to the next PTT ON (estimate of remote transmit plus dead time) |
| `buffer` | each BUFFER report: `t`, `bytes` |
| `states` | ARDOP NEWSTATE and STATUS lines: `t`, `line` |
| `messages` | each binary transfer: `dir`, `title`, `offset`, `compressed_size`, `complete` |
| `errors` | `***` lines and broken transfers: `t`, `dir`, `text` |

`compressed_size` counts the data bytes in that transfer, which start at `offset` when the
transfer resumes an earlier one. `complete` is true when the transfer ended with a good
checksum. Titles are kept; message contents are not.

## How it works

Pat opens two TCP connections to the modem, one for commands and one for data. The proxy
accepts both and opens matching ones to the modem. It hands each chunk it reads to the tracker
before writing it on, so it has seen every event before the other side can react.

- **Command port:** lines ending in `\r` both ways. CONNECTED starts a session, DISCONNECTED
  (or ARDOP's `NEWSTATE DISC`) ends it. The modem sends the last data and DISCONNECTED on
  different connections, so they can arrive in either order; a session stays open two more
  seconds for late data.
- **Data port:** VARA passes raw bytes. ARDOP frames them: host to TNC is a 2-byte big-endian
  length and the data, TNC to host is a length, a 3-letter type and the data. Only `ARQ`
  frames carry the B2F session.
- **B2F:** each direction is parsed byte by byte, so read boundaries do not matter. An element
  that starts with SOH (0x01) is a binary transfer: title, offset, STX blocks, then EOT and a
  checksum. Anything else is a text line ending in `\r`. A turn is a run of data in one
  direction; every change of direction is a turnaround.

## Building from source

```
git clone https://github.com/Londopy/b2trace
cd b2trace
go build
go test ./...
```

## Layout

```
main.go       command dispatch and usage
proxy.go      listeners, relaying, command-port lines
tracker.go    turns what the proxy sees into sessions
b2f.go        the passive B2F parser
ardop.go      ARDOP data-port framing
session.go    the session written to the JSONL log
summary.go    b2trace summary
compress.go   b2trace compress
testdata/     JSONL input for the summary test
```

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Add a line under
`Unreleased` in [CHANGELOG.md](CHANGELOG.md) with any change.

## License

MIT, see [LICENSE](LICENSE).
