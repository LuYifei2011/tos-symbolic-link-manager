#!/usr/bin/env python3
"""Create an independent consumer project; no links to the template or kit."""
import argparse
import json
from pathlib import Path
import shutil
from metadata import ROOT, load, sync, validate


def create(destination, config):
    validate(config)
    destination = destination.resolve()
    if destination == ROOT or ROOT in destination.parents:
        raise ValueError("destination must be outside the template directory")
    destination.mkdir(parents=True, exist_ok=False)
    try:
        shutil.copytree(ROOT, destination, dirs_exist_ok=True, ignore=shutil.ignore_patterns(
            ".git", "node_modules", "dist", "build", ".local", "__pycache__", "*.pyc", ".DS_Store", "anti-slop"))
        (destination / "app.json").write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n")
        sync(destination)
    except BaseException:
        shutil.rmtree(destination)
        raise
    return destination


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path)
    parser.add_argument("--id", required=True)
    parser.add_argument("--name", required=True, help="Chinese display name")
    parser.add_argument("--english-name", required=True)
    parser.add_argument("--publisher", required=True)
    parser.add_argument("--email", required=True)
    parser.add_argument("--help-url", required=True)
    args = parser.parse_args()
    config = load()
    config.update(id=args.id, publisher=args.publisher, email=args.email, help=args.help_url)
    config["name"] = {"zh-CN": args.name, "en-US": args.english_name}
    try:
        destination = create(args.destination, config)
    except (ValueError, FileExistsError) as error:
        parser.exit(1, str(error) + "\n")
    print(f"Created {destination}\nNext: npm ci, go mod download, npm run build")


if __name__ == "__main__":
    main()
