#!/usr/bin/env python3
"""Refresh the bundled IEEE registry snapshot; runtime never contacts IEEE."""
import csv
import gzip
import hashlib
import io
import json
from datetime import datetime, timezone
from pathlib import Path
from urllib.request import urlopen
import argparse

parser = argparse.ArgumentParser()
parser.add_argument("--source-dir", type=Path, help="use previously downloaded CSVs")
args = parser.parse_args()
rows, sources = {}, []
for name, path, width in [("oui.csv", "oui/oui.csv", 6), ("mam.csv", "oui28/mam.csv", 7), ("oui36.csv", "oui36/oui36.csv", 9)]:
    url = "https://standards-oui.ieee.org/" + path
    if args.source_dir:
        raw = (args.source_dir / name).read_bytes()
    else:
        with urlopen(url, timeout=60) as response:
            raw = response.read(32 * 1024 * 1024)
    entries = list(csv.DictReader(io.StringIO(raw.decode("utf-8-sig"))))
    if len(entries) < 1000:
        raise ValueError("incomplete registry: " + name)
    for entry in entries:
        prefix = entry["Assignment"].strip().upper()
        if len(prefix) != width or any(c not in "0123456789ABCDEF" for c in prefix):
            raise ValueError("invalid assignment: " + prefix)
        rows[prefix] = entry["Organization Name"].strip()
    sources.append({"url": url, "sha256": hashlib.sha256(raw).hexdigest(), "records": len(entries)})
out = io.StringIO(newline="")
writer = csv.writer(out, lineterminator="\n")
writer.writerow(["prefix", "vendor"])
writer.writerows(sorted(rows.items()))
root = Path(__file__).resolve().parents[1] / "internal/monitor"
(root / "vendors.csv.gz").write_bytes(gzip.compress(out.getvalue().encode(), mtime=0))
(root / "vendors.sources.json").write_text(json.dumps({"retrieved_at": datetime.now(timezone.utc).isoformat(), "sources": sources, "records": len(rows)}, indent=2) + "\n")
print(f"Bundled {len(rows)} IEEE assignments")
