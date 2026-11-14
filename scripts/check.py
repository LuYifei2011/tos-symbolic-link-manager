#!/usr/bin/env python3
import argparse
from metadata import check

parser = argparse.ArgumentParser()
parser.add_argument("--release", action="store_true")
parser.add_argument("--tag")
args = parser.parse_args()
try:
    config = check(release=args.release, tag=args.tag)
except (ValueError, KeyError) as error:
    parser.exit(1, str(error) + "\n")
print(f"PASS metadata: {config['id']} {config['version']}")
