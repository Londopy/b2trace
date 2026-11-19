package main

import (
	"bytes"
	"testing"
)

// The numbers below are worked out by hand from testdata/sessions.jsonl.
const wantSummary = `session 1: vara, 2026-11-19 18:02:11, 3m10.4s, local N0CALL, remote LA1B, ended: disconnected

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

session 2: ardop, 2026-11-19 19:00:00, 45.5s, remote W1AW, ended: data connection closed

bytes              out        in      both   share
handshake           90       120       210   26.8%
proposals            0        40        40    5.1%
payload              0       500       500   63.9%
answers/end          3         0         3    0.4%
other                0        30        30    3.8%
total               93       690       783  100.0%

turns             3
turnarounds       2
PTT intervals     0
local TX          0.0 s
gaps              0
mean gap          -
possible retries  0 (BUFFER non-zero and unchanged for 2+ samples)
messages          0 out, 1 in
errors            1

total: 2 sessions, 3m55.9s

bytes              out        in      both   share
handshake          190       270       460   15.4%
proposals           60        90       150    5.0%
payload           1800       500      2300   77.1%
answers/end          7         9        16    0.5%
other                0        57        57    1.9%
total             2057       926      2983  100.0%

turns             9
turnarounds       7
PTT intervals     3
local TX          12.3 s
gaps              2
mean gap          2.50 s (estimate: local PTT off to next PTT on)
possible retries  2 (BUFFER non-zero and unchanged for 2+ samples)
messages          1 out, 1 in
errors            1
`

func TestSummary(t *testing.T) {
	var out bytes.Buffer
	if err := runSummary([]string{"testdata/sessions.jsonl"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != wantSummary {
		t.Errorf("got:\n%s\nwant:\n%s", got, wantSummary)
	}
}
