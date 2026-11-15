"""Shared manifest validation and TOS package metadata."""
import json
import re
from pathlib import Path
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parent.parent
RESERVED_IDS = {"docker", "bin", "var", "api", "usr", "root", "admin", "system", "service"}


def validate(config):
    expected = {"id", "version", "name", "description", "publisher", "email", "help", "beta"}
    if not isinstance(config, dict) or set(config) != expected:
        raise ValueError("app.json must contain exactly: " + ", ".join(sorted(expected)))
    app_id = config["id"]
    if not isinstance(app_id, str) or not re.fullmatch(r"[a-z][a-z0-9-]{0,22}[a-z0-9]", app_id) or app_id.startswith("tos"):
        raise ValueError("id must be 2-24 lowercase letters/digits/hyphens, start with a letter, end with a letter/digit, and not start with tos")
    if app_id in RESERVED_IDS:
        raise ValueError("id cannot be a reserved system keyword")
    if not isinstance(config["version"], str) or not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", config["version"]):
        raise ValueError("version must have three numeric components, e.g. 0.1.0")
    for key in ("name", "description"):
        value = config[key]
        if not isinstance(value, dict) or set(value) != {"zh-CN", "en-US"}:
            raise ValueError(f"{key} needs zh-CN and en-US")
        for text in value.values():
            validate_text(text, key, 300)
    for key in ("publisher", "email", "help"):
        validate_text(config[key], key, 300)
    if not re.fullmatch(r"[^\s<>@]+@[^\s<>@]+\.[^\s<>@]+", config["email"]):
        raise ValueError("email must be a maintainer email address")
    if any(char in config["publisher"] for char in "<>"):
        raise ValueError("publisher cannot contain angle brackets")
    help_url = urlsplit(config["help"])
    if help_url.scheme != "https" or not help_url.hostname or help_url.username or help_url.password or any(ch.isspace() for ch in config["help"]):
        raise ValueError("help must be an absolute HTTPS URL without credentials")
    if type(config["beta"]) is not bool:
        raise ValueError("beta must be true or false")
    return config


def validate_text(value, name, limit):
    if not isinstance(value, str) or not value.strip() or len(value) > limit or any(ord(ch) < 32 or ord(ch) == 127 for ch in value):
        raise ValueError(f"{name} must be a nonempty single line, at most {limit} characters")


def load(root=ROOT):
    return validate(json.loads((root / "app.json").read_text(encoding="utf-8")))


def check(root=ROOT, release=False, tag=None):
    config = load(root)
    for name in ("package.json", "package-lock.json"):
        package = json.loads((root / name).read_text())
        if package["name"] != config["id"] or package["version"] != config["version"]:
            raise ValueError(f"{name} identity differs from app.json; run scripts/sync-metadata.py")
        if name == "package-lock.json":
            item = package["packages"][""]
            if item["name"] != config["id"] or item["version"] != config["version"]:
                raise ValueError("package-lock.json root identity differs from app.json")
    if tag is not None and tag != "v" + config["version"]:
        raise ValueError("release tag must equal v" + config["version"])
    if release:
        if config["id"] == "sample-app" or config["publisher"] == "Your Name" or config["email"].endswith("@example.com") or urlsplit(config["help"]).hostname == "example.com":
            raise ValueError("replace the sample application identity before releasing")
        if "Placeholder application icon" in (root / "packaging/icon.svg").read_text():
            raise ValueError("replace packaging/icon.svg before releasing")
    return config


def sync(root=ROOT):
    config = load(root)
    for name in ("package.json", "package-lock.json"):
        target = root / name
        if not target.exists():
            continue
        package = json.loads(target.read_text())
        package.update(name=config["id"], version=config["version"])
        if name == "package-lock.json":
            package["packages"][""].update(name=config["id"], version=config["version"])
        target.write_text(json.dumps(package, ensure_ascii=False, indent=2) + "\n")


def tos_config(config, arch):
    app_id = config["id"]
    return {
        "id": app_id, "icon": f"/images/icons/{app_id}.svg", "publisher": config["publisher"],
        "exec": True, "type": "iframe", "path": f"/{app_id}/", "resize": True, "maxmin": True,
        "width": 960, "height": 600, "version": config["version"], "recommend": False,
        "beta": config["beta"], "low_version": "TOS7.0", "category": ["Utilities"], "relation": [],
        "platform": {"amd64": "x86_64", "arm64": "aarch64"}[arch], "application_type": "deb",
        "system_id": app_id, "package": app_id, "user": app_id, "allow_open_in_mobile": False, "help": config["help"],
    }


def language_file(config):
    # TOS expects each language section. Untranslated entries use the supplied English text.
    locales = "zh-cn zh-hk en-us ko-kr ja-jp de-de fr-fr it-it es-es pt-pt cs-cz da-dk nl-nl nb-no sv-se el-gr ro-ro hu-hu tr-tr pl-pl ru-ru uk-ua th-th vi-vn id-id ar-sa he-il".split()
    sections = []
    for locale in locales:
        lang = "zh-CN" if locale == "zh-cn" else "en-US"
        values = {"name": config["name"][lang], "auth": config["publisher"], "descript": config["description"][lang], "release_note": config["version"], "important": ""}
        sections.append("[" + locale + "]\n" + "\n".join(f"{key} = {json.dumps(value, ensure_ascii=False)}" for key, value in values.items()))
    return "\n\n".join(sections) + "\n"
