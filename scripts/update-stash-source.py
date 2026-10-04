#!/usr/bin/env python3
"""Write the companion index from the exact release archive, never a stale checksum."""
import argparse
import datetime
import hashlib
import pathlib
import re

parser = argparse.ArgumentParser()
parser.add_argument("--tag", required=True)
parser.add_argument("--archive", required=True)
parser.add_argument("--output", default="stash-plugin-source.yml")
args = parser.parse_args()
if not re.fullmatch(r"v\d+\.\d+\.\d+", args.tag):
    parser.error("expected a release version tag")
archive = pathlib.Path(args.archive)
source = pathlib.Path("stash-plugin-source.yml").read_text()
values = {
    "date": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M:%S"),
    "path": f"https://github.com/Net005/silo-plugin-metadata-stash/releases/download/{args.tag}/{archive.name}",
    "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
}
for key, value in values.items():
    source, count = re.subn(rf"(?m)^  {key}: .*$", f"  {key}: {value}", source)
    if count != 1:
        raise ValueError(f"expected one {key} entry")
pathlib.Path(args.output).write_text(source)
