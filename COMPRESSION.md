# Compression

How much smaller Winlink messages get with other compressors than B2F's LZHUF, measured with
`b2trace compress` on the corpus in [`corpus/`](corpus/).

## The corpus

74 complete Winlink messages, headers and attachments included, since B2F compresses the
whole message. Each file is byte for byte what wl2k-go, the library Pat uses, writes for that
message. Everything was written for this repository by
[`scripts/make_corpus.py`](scripts/make_corpus.py), so there is no question of rights to use
it. Callsigns are N0 calls with four-letter suffixes, which are never issued, and names are
roles, not people.

| Kind | Messages | What it is |
|---|---|---|
| ics213 | 14 | ICS-213 general messages: a text body and the form's XML as an attachment, filled with made-up data, between the ten stations of one exercise |
| position | 14 | position reports in the layout Pat writes, from the same stations |
| radiogram | 10 | radiograms in the usual NR / BT layout, each between two stations seen nowhere else |
| checkin | 10 | one-line net check-ins to net control |
| weather | 10 | spotter reports to net control |
| prose | 16 | plain messages written by hand, from one line to a paragraph, each between two stations seen nowhere else |

## Results

Median size relative to LZHUF (lower is smaller), per kind and over all 74 messages. The
dictionary builder is not deterministic, so the zstd+dict figures are the median of 9 runs,
with the range in brackets; every other column is the same on every run. The last column is
how much smaller zstd+dict is than brotli.

| Kind | Messages | Median raw size | gzip | brotli | zstd | zstd+dict | zstd+dict vs brotli |
|---|---|---|---|---|---|---|---|
| checkin | 10 | 274 | 104.4% | 81.7% | 100.9% | 47.7% (47.0 to 48.7) | 42% smaller (40 to 42) |
| position | 14 | 352 | 101.6% | 78.6% | 97.9% | 48.0% (46.1 to 49.7) | 39% smaller (37 to 41) |
| prose | 16 | 371 | 99.8% | 75.1% | 97.4% | 66.6% (66.3 to 67.3) | 11% smaller (10 to 12) |
| radiogram | 10 | 386 | 99.5% | 77.9% | 97.8% | 60.3% (59.8 to 60.6) | 23% smaller (22 to 23) |
| weather | 10 | 429 | 100.0% | 75.7% | 96.3% | 50.7% (50.1 to 52.3) | 33% smaller (31 to 34) |
| ics213 | 14 | 1345 | 96.9% | 74.1% | 97.3% | 45.2% (43.7 to 47.4) | 39% smaller (36 to 41) |
| all | 74 | 382 | 100.0% | 77.3% | 97.6% | 49.9% (49.1 to 50.3) | 35% smaller (35 to 36) |

In total bytes, the 74 messages are 40547 raw, 27004 with LZHUF, 26852 with gzip, 26379 with
zstd, 20683 with brotli, and 14011 with zstd and a dictionary (13921 to 14145).

`compress` trains each dictionary on half the corpus, about 20 KB of messages, and the
builder stops at about 3.5 KB on that, whatever `--dict-size` above that allows; `compress`
prints the sizes it trained. Capping the dictionary lower costs a lot: in single runs, the
median message comes to 57.5% of LZHUF with a 2 KB dictionary and 63.6% with 1 KB.

To repeat the measurement:

```
python scripts/make_corpus.py
b2trace compress corpus
python scripts/compression_table.py b2trace 9
```

The first rebuilds the corpus (the committed files are the reference, since another Python
version may draw other random values), the second prints the sizes per file, and the third
runs `compress --csv` 9 times and prints the table above.

## What this says

This work set itself two targets: at least 25% fewer bytes than LZHUF on typical short
messages, and a trained dictionary only if it beats brotli by 10% or more on the median
message, since both ends would then have to ship the same dictionary.

- **gzip** is no better than LZHUF on messages this short.
- **brotli** at quality 11 makes the median message 23% smaller than LZHUF, just short of
  the target. Only the ICS-213 messages pass it, at 26%. Prose comes out 24.9% smaller,
  spotter reports 24.3%, radiograms 22%, position reports 21% and check-ins, the shortest,
  18%.
- **zstd with a trained dictionary** beats brotli by 35% on the median message. The margin is
  largest where messages share the most: forms, reports and check-ins between the same few
  stations. On the prose, where only the header layout repeats and every message has its own
  stations and subject, it is 11%, just past the bar.

Each message's dictionary was trained on the other half of the corpus, never on the message
itself. Even so, the forms and reports here come from templates and repeat each other more
than real traffic would, so they flatter the dictionary. The prose row is the closest to a
fair test, and there the dictionary only just clears 10%.

So this corpus cannot settle it. That needs real messages, shared with their senders'
consent, and a plan for the costs of a dictionary: both ends ship the same versioned
dictionary, and its ID travels in the proposal so it can change later. Until then brotli,
which needs no shared dictionary, stays the compressor in the proposed FE extension.
