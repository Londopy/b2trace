# Results

Measured B2F sessions that people have chosen to share, one row per link label. Each row
comes from the files in `results/`, made with `b2trace share`, which removes callsigns,
locators, addresses, message titles and MIDs.

No sessions have been shared yet.

## Columns

- **Turnarounds (median):** changes of direction per session. B2F needs about 8 to move one
  message each way, and a proposed B2F extension that sends small messages without waiting
  for an answer aims to cut that to 4 or 5.
- **Gap (mean):** from your PTT going off to it coming on again: the remote's transmission
  plus the turnaround dead time. VARA and ARDOP only.
- **Connect (median):** from Pat's connect command to CONNECTED, for sessions you started.
- **Payload share of time:** the estimated share of the session spent moving messages. The
  README explains how the time is split.
- **Payload share of bytes:** the same for bytes. The gap between the two is what the
  handshake, proposals and turnarounds cost.

## Adding yours

1. Record some sessions with `b2trace proxy`, with a `--link` label for the mode and
   bandwidth, such as `varahf-2300`, `varafm`, `ardop-500` or `packet1200`.
2. Strip them:
   ```
   b2trace share -o results/varahf-2300-2026-11-19.jsonl sessions.jsonl
   ```
3. Read the file. It keeps your link labels and the session times. If anything in it should
   not be public, leave that session out.
4. Regenerate the table and paste it above, in place of the line that says nothing has been
   shared:
   ```
   b2trace results results/*.jsonl
   ```
5. Open a pull request with the new file and the table. CI checks that every file in
   `results/` went through `share` and that this page shows the table they give.
