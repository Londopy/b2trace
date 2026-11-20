package main

import (
	"bytes"
	"testing"
)

// The numbers below are worked out by hand from testdata/sessions.jsonl.
const wantSummary = `session 1: vara (varahf), 2026-11-19 18:02:11, 3m10.4s, local N0CALL, remote N0GATE, ended: disconnected
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

session 2: ardop, 2026-11-19 19:00:00, 45.5s, remote N0RMTE, ended: data connection closed

bytes              out        in      both   share
handshake           90       120       210   26.8%
proposals            0        40        40    5.1%
payload              0       500       500   63.9%
answers/end          3         0         3    0.4%
other                0        30        30    3.8%
total               93       690       783  100.0%

time           seconds   share  (estimate, see README)
connect            0.0    0.0%
setup              2.0    4.4%
handshake          5.2   11.3%
proposals          0.8    1.6%
payload           30.2   66.3%
answers/end        0.1    0.2%
other              1.8    4.0%
disconnect         5.5   12.1%
total             45.5  100.0%

turns             3
turnarounds       2
PTT intervals     0
local TX          0.0 s
gaps              0
mean gap          -
possible retries  0 (BUFFER non-zero and unchanged for 2+ samples)
proposals out     0
proposals in      1 (FC 1): 1 not answered
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

time           seconds   share  (estimate, see README)
connect            5.0    2.1%
setup              3.0    1.2%
handshake         10.5    4.4%
proposals          3.8    1.6%
payload          208.1   86.4%
answers/end        0.3    0.1%
other              2.7    1.1%
disconnect         7.5    3.1%
total            240.9  100.0%

turns             9
turnarounds       7
PTT intervals     3
local TX          12.3 s
gaps              2
mean gap          2.50 s (estimate: local PTT off to next PTT on)
possible retries  2 (BUFFER non-zero and unchanged for 2+ samples)
proposals out     1 (FC 1): 1 accepted
proposals in      1 (FC 1): 1 not answered
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
