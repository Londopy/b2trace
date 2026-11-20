"""Prints the tables in COMPRESSION.md from repeated b2trace compress runs.

usage: python scripts/compression_table.py [b2trace] [runs]

Runs `b2trace compress --csv corpus` the given number of times (default 9).
Every column but zstd+dict comes out the same each run. The dictionary
builder is not deterministic, so for zstd+dict the tables give the median
over the runs and the range in brackets.
"""

import collections
import csv
import io
import re
import statistics
import subprocess
import sys

COLUMNS = ["gzip", "brotli", "zstd", "zstd+dict"]


def run(b2trace):
    out = subprocess.run([b2trace, "compress", "--csv", "corpus"], capture_output=True, text=True, check=True).stdout
    return [r for r in csv.DictReader(io.StringIO(out)) if r["file"].endswith(".b2f")]


def kind(row):
    return re.sub(r"-\d+\.b2f$", "", re.split(r"[\\/]", row["file"])[-1])


def medians(rows):
    """Median size relative to LZHUF, in percent, per kind and over all."""
    groups = collections.defaultdict(list)
    for r in rows:
        groups[kind(r)].append(r)
        groups["all"].append(r)
    return {k: {c: statistics.median(int(r[c]) / int(r["lzhuf"]) * 100 for r in rs) for c in COLUMNS}
            for k, rs in groups.items()}, groups


def main():
    b2trace = sys.argv[1] if len(sys.argv) > 1 else "b2trace"
    runs = int(sys.argv[2]) if len(sys.argv) > 2 else 9
    all_runs = [run(b2trace) for _ in range(runs)]
    first, groups = medians(all_runs[0])
    dict_runs = [medians(rows)[0] for rows in all_runs]
    totals = [sum(int(r["zstd+dict"]) for r in rows) for rows in all_runs]

    order = sorted((k for k in groups if k != "all"), key=lambda k: statistics.median(int(r["raw"]) for r in groups[k]))
    print("| Kind | Messages | Median raw size | gzip | brotli | zstd | zstd+dict | zstd+dict vs brotli |")
    print("|---|---|---|---|---|---|---|---|")
    for k in order + ["all"]:
        d = [m[k]["zstd+dict"] for m in dict_runs]
        mid = statistics.median(d)
        vs = [(1 - x / first[k]["brotli"]) * 100 for x in d]
        print("| %s | %d | %.0f | %.1f%% | %.1f%% | %.1f%% | %.1f%% (%.1f to %.1f) | %.0f%% smaller (%.0f to %.0f) |" % (
            k, len(groups[k]), statistics.median(int(r["raw"]) for r in groups[k]),
            first[k]["gzip"], first[k]["brotli"], first[k]["zstd"], mid, min(d), max(d),
            (1 - mid / first[k]["brotli"]) * 100, min(vs), max(vs)))
    rows = all_runs[0]
    print()
    print("Totals: raw %d, lzhuf %d, gzip %d, brotli %d, zstd %d, zstd+dict %d (%d to %d) over %d runs" % (
        sum(int(r["raw"]) for r in rows), sum(int(r["lzhuf"]) for r in rows), sum(int(r["gzip"]) for r in rows),
        sum(int(r["brotli"]) for r in rows), sum(int(r["zstd"]) for r in rows),
        statistics.median(totals), min(totals), max(totals), runs))


if __name__ == "__main__":
    main()
