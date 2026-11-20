# b2trace

[![CI](https://github.com/Londopy/b2trace/actions/workflows/ci.yml/badge.svg)](https://github.com/Londopy/b2trace/actions/workflows/ci.yml)
[![changelog](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/Londopy/b2trace/gh-pages/changelog-badge.json)](CHANGELOG.md)
[![License](https://img.shields.io/github/license/Londopy/b2trace)](LICENSE)

**See where the airtime goes in a Winlink B2F session.**

b2trace sits between Pat (or any Winlink client) and a VARA or ARDOP sound-card modem, or a
packet TNC such as Direwolf, as a passive TCP proxy. It forwards every byte unchanged, follows
the B2F protocol in both directions, and writes one JSON line per session: every modem command,
how many bytes and roughly how much time went to the handshake, the proposals, the messages and
the answers, how often the link turned around, how long your own transmitter was keyed, and how
each proposal was answered. Other commands summarize the log, strip it for sharing, and compare
compressors on Winlink messages.

A session in `b2trace summary` looks like this (numbers from the test data, not a real link):

```
session 1: vara (varahf), 2026-11-19 18:02:11, 3m10.4s, local N0CALL, remote LA1B, ended: disconnected
sid out [PAT-0.17.0-B2FHM$]
sid in  [WL2K-5.0-B2FWIHJM$]

bytes              out        in      both   share
handshake          100       150       250   11.4%
proposals           60        50       110    5.0%
payload           1800         0      1800   81.8%
answers/end          4         9        13    0.6%
other                0        27        27    1.2%
total             1964       236      2200  100.0%

time           seconds   share  (estimate, see README)
connect            5.0    2.6%
setup              1.0    0.5%
handshake          5.4    2.8%
proposals          3.1    1.6%
payload          177.9   91.0%
answers/end        0.2    0.1%
other              0.9    0.5%
disconnect         2.0    1.0%
total            195.4  100.0%

turns             6
turnarounds       5
PTT intervals     3
local TX          12.3 s
gaps              2
mean gap          2.50 s (estimate: local PTT off to next PTT on)
possible retries  2 (BUFFER non-zero and unchanged for 2+ samples)
proposals out     1 (FC 1): 1 accepted
proposals in      0
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

The proxy takes the modem's usual port, so Pat needs no change, and passes everything on to
the modem on a port you move it to.

**VARA HF and VARA FM**

VARA uses one port for commands and the next one for data, so each address below stands for a
pair.

1. In VARA's settings, move its TCP ports from 8300 and 8301 to 18300 (command) and 18301 (data).
2. Start the proxy:
   ```
   b2trace proxy --modem vara --target 127.0.0.1:18300 --link varahf
   ```
3. Use Pat as usual. Its `varahf` or `varafm` address stays `localhost:8300`, which is now the
   proxy.

VARA HF and VARA FM speak the same protocol, so b2trace cannot tell them apart. `--link` puts a
label of your choice on each session (`varahf`, `varafm`, `varahf-500`), which the results
table groups by. Without it, sessions are grouped by modem type.

**ARDOP**

1. Start the ARDOP TNC on port 18515 instead of 8515 (ardopc and ardopcf take the port as their
   first argument).
2. Start the proxy:
   ```
   b2trace proxy --modem ardop --target 127.0.0.1:18515
   ```
3. Pat's `ardop` address stays `localhost:8515`.

With ARDOP, Pat finds the data port by adding one to the last digit of the address, so if you
pass another address to `--listen`, pick a port that does not end in 9.

**Packet, over AGWPE (Direwolf, QtSoundModem, soundmodem)**

AGWPE carries commands and data on one port.

1. Move the TNC's AGWPE port from 8000 to 18000. In `direwolf.conf` that is `AGWPORT 18000`.
2. Start the proxy:
   ```
   b2trace proxy --modem agwpe --target 127.0.0.1:18000 --link packet1200
   ```
3. Pat's `agwpe` address stays `localhost:8000`.

The TNC does not tell its clients when it keys the transmitter, so packet sessions have no
local TX time, gaps or retry hints. Everything else is recorded. Other programs can share the
TNC through the proxy, such as an APRS client; their frames are relayed but kept out of the
session Pat is in.

If Pat is set to another address, pass that address to `--listen`.

The proxy prints every command with a timestamp (on AGWPE, every frame other than data and
the frame counts Pat polls for several times a second). It appends a session to
`sessions.jsonl` (change it with `--log`) two seconds after the modem reports the disconnect,
when a connection to the modem closes, or when you stop the proxy with Ctrl+C. If the modem is
not running, the proxy closes Pat's connection and prints the address it could not reach.

### Read the results

```
b2trace summary sessions.jsonl
```

It takes one log or several. For each session and for all of them together:

- the SIDs both sides sent, which show the protocol features each one offered
- bytes per phase in each direction and their share of all bytes
- an estimate of the time per phase (below)
- turns and turnarounds, local transmit time, and the mean gap between your transmissions
- possible retries: times the modem's BUFFER report stayed the same and non-zero over two or
  more reports
- the proposals each way by code (FC is LZHUF, FD gzip, FE brotli), and how the other side
  answered them: accepted, resumed from an offset, deferred, rejected, or not answered before
  the link dropped

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

**Time per phase.** The time table splits the whole session, from the connect command to the
disconnect, into:

- `connect`: from Pat's connect command to the modem's CONNECTED (0 for a session the remote
  started)
- `setup`: from CONNECTED to the first B2F byte
- one row per phase: each turn lasts from its first byte to the first byte of the next turn,
  so it takes in the turnaround and the start of the reply, and its time is shared between its
  phases by bytes
- `disconnect`: from the last B2F byte to the end of the session

Because it is built from when bytes passed the proxy, it works the same on every modem, but it
is an estimate: see the next section for what one end of a link can and cannot see.

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

### Share your numbers

[RESULTS.md](RESULTS.md) collects sessions that people choose to share, so the numbers come
from real links. To add yours:

```
b2trace share -o varahf-2026-11-19.jsonl sessions.jsonl
```

`share` removes callsigns, locators, IP and email addresses, message titles and MIDs. It keeps
the numbers, the SIDs, the protocol keywords, your `--link` labels as you wrote them, and the
times, with your time zone's offset. Look the file over, then open a pull request that adds it
to `results/`. Your logs never leave your machine unless you do this. (Use `-o` rather than
`>`: Windows PowerShell 5 writes UTF-16 through `>`, which b2trace refuses to read.)

`b2trace results results/*.jsonl` prints the table on the results page, one row per link
label. It only reads files made by `share`. Patterns like `*.jsonl` work in PowerShell and cmd
too, which leave them to the program.

## What one side can and cannot measure

b2trace sees only your end of the link.

- **Exact:** your own transmit time, from the PTT messages the modem sends on its command port
  (`PTT ON`/`PTT OFF` from VARA, `PTT TRUE`/`PTT FALSE` from ARDOP). If your setup sends none,
  `local_tx_ms` stays 0 and there are no gaps. AGWPE never sends them.
- **Exact:** B2F bytes per phase and direction, turns, message sizes, proposals and answers,
  and the modem's commands and BUFFER reports as it sent them.
- **Estimated:** the remote station's transmit time and the turnaround dead time. b2trace never
  sees the remote's PTT. All it has is `gap_ms`, the time from your PTT going off to it coming
  on again: the remote's transmission plus the dead time on both sides of it. One station
  cannot split those.
- **Estimated:** the time per phase. Outgoing bytes can wait in the modem's buffer before they
  go on the air, and incoming bytes show up only after the modem has decoded them, so a turn's
  time as the proxy sees it is shifted from its airtime. Turns that mix phases share their time
  by bytes, which is only a rough guide.
- **Not airtime:** byte shares. VARA compresses data itself, all of these modems add framing
  and acknowledgements, and their speed changes with conditions, so a phase's share of the
  bytes is not its share of the seconds.
- **A hint, not a count:** possible retries. A BUFFER report that repeats a non-zero value means
  nothing was acknowledged in between, which is often a repeated frame, but the modem does not
  say so.

## JSON fields

One line per session. Times are RFC 3339 with milliseconds, in the time zone of the machine
running the proxy. `out` is what your station sent, `in` is what it received.

| Field | Meaning |
|---|---|
| `modem` | `vara`, `ardop` or `agwpe` |
| `link` | the `--link` label; left out when not set |
| `shared` | true in a file made by `b2trace share` |
| `start`, `end` | when the proxy saw CONNECTED and the disconnect, or when the session was cut short |
| `end_reason` | `disconnected`, `data connection closed`, `command connection closed`, `API connection closed`, `new CONNECTED` or `proxy stopped` |
| `connect_line` | the modem's CONNECTED line as sent, with the bandwidth; on AGWPE, the connect frame written as in `commands` |
| `connect_ms` | time from Pat's connect command to CONNECTED; left out for sessions the remote started |
| `local_call`, `remote_call` | from MYCALL (or the AGWPE registration) and the CONNECTED line; left out when unknown |
| `sid_out`, `sid_in` | the first SID each side sent: a handshake line in brackets that ends in `$]` |
| `bytes_out`, `bytes_in` | B2F bytes per phase: `handshake`, `pending`, `proposals`, `answer`, `payload`, `end`, `error`, `other` |
| `turn_count`, `turnarounds` | runs of data in one direction, and the changes of direction between them |
| `turns` | each turn: `dir`, `first_byte`, `last_byte` and `bytes` per phase |
| `local_tx_ms` | total time your PTT was on (exact) |
| `ptt` | each PTT interval: `on`, `off`, `local_tx_ms` |
| `gap_ms` | each time from a PTT OFF to the next PTT ON (estimate of remote transmit plus dead time) |
| `buffer` | each BUFFER report: `t`, `bytes`; over AGWPE, each change in the frames waiting for an acknowledgement |
| `buffer_unit` | `frames` for AGWPE; left out for bytes |
| `states` | ARDOP NEWSTATE and STATUS lines: `t`, `line` |
| `commands` | every command-port line but the BUFFER reports, from the end of the previous session (up to 50 lines before CONNECTED) to the end of this one: `t`, `dir`, `line`. Lines Pat sends after the disconnect go with the next session. On AGWPE, one line per frame on Pat's connection other than data and frame counts: kind, port, from, to, then the text of a connect or disconnect frame or the length of other data |
| `proposals` | each FA to FE line: `dir`, `code`, `mid`, `size`, `compressed_size`, `optimistic` (Z extension), and the other side's `answer` (`accept`, `reject`, `defer`, `resume`, or `other` for a letter b2trace does not know) with the resume `offset` |
| `messages` | each binary transfer: `dir`, `title`, `offset`, `compressed_size`, `complete` |
| `errors` | `***` lines and broken transfers: `t`, `dir`, `text` |

`compressed_size` in `messages` counts the data bytes in that transfer, which start at `offset`
when the transfer resumes an earlier one. `complete` is true when the transfer ended with a good
checksum. Titles are kept in your own log; message contents never are.

## How it works

Pat opens a command and a data connection to VARA and ARDOP, and one connection to an AGWPE
TNC. The proxy accepts each one and opens a matching one to the modem. It hands each chunk it
reads to the tracker before writing it on, so it has seen every event before the other side
can react.

- **Command port:** lines ending in `\r` both ways. CONNECTED starts a session, DISCONNECTED
  (or ARDOP's `NEWSTATE DISC`) ends it. The modem sends the last data and DISCONNECTED on
  different connections, so they can arrive in either order; a session stays open two more
  seconds for late data.
- **Data port:** VARA passes raw bytes. ARDOP frames them: host to TNC is a 2-byte big-endian
  length and the data, TNC to host is a length, a 3-letter type and the data. Only `ARQ`
  frames carry the B2F session.
- **AGWPE:** every frame has a 36-byte header with its kind, the two callsigns and the data
  length. `D` frames carry the session; a `C` frame from the TNC starts it, `d` ends it, and `Y`
  answers say how many frames are waiting for an acknowledgement.
- **B2F:** each direction is parsed byte by byte, so read boundaries do not matter. An element
  that starts with SOH (0x01) is a binary transfer: title, offset, STX blocks, then EOT and a
  checksum. Anything else is a text line ending in `\r`. A turn is a run of data in one
  direction; every change of direction is a turnaround. An FS line answers the open proposals
  from the other side, in order.

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
agwpe.go      AGWPE framing
ardop.go      ARDOP data-port framing
tracker.go    turns what the proxy sees into sessions
b2f.go        the passive B2F parser
session.go    the session written to the JSONL log
summary.go    b2trace summary
share.go      b2trace share
results.go    b2trace results
compress.go   b2trace compress
testdata/     JSONL input for the summary test
```

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Add a line under
`Unreleased` in [CHANGELOG.md](CHANGELOG.md) with any change.

## License

MIT, see [LICENSE](LICENSE).
