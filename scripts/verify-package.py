#!/usr/bin/env python3
"""Verify the built deb against the manifest, frontend and runtime contract."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import struct
import tarfile
from metadata import ROOT, check, language_file, tos_config


def verify(package):
    config = check()
    app_id = config["id"]
    data = package.read_bytes()
    assert data[:8] == b"!<arch>\n", "not a deb archive"
    members, pos = {}, 8
    while pos < len(data):
        header = data[pos:pos+60]
        assert len(header) == 60 and header[-2:] == b"`\n", "invalid ar header"
        size = int(header[48:58])
        name = header[:16].decode().strip().rstrip("/")
        assert name not in members and pos + 60 + size <= len(data), "duplicate or truncated ar member"
        members[name] = data[pos+60:pos+60+size]
        pos += 60 + size + size % 2
    assert members["debian-binary"] == b"2.0\n"
    for name in ("control.tar.gz", "data.tar.gz"):
        assert gzip.decompress(members[name])[257:265] == b"ustar  \x00", "GNU tar required"
    with tarfile.open(fileobj=io.BytesIO(members["control.tar.gz"])) as control:
        fields = control.extractfile("./control").read().decode()
        arch = re.search(r"^Architecture: (amd64|arm64)$", fields, re.M)[1]
        assert f"Package: {app_id}\n" in fields and f"Version: {config['version']}\n" in fields
        assert f"Maintainer: {config['publisher']} <{config['email']}>\n" in fields
        for name in ("postinst", "prerm", "postrm"):
            expected = (ROOT / "packaging/debian" / f"{name}.in").read_text().replace("@APP_ID@", app_id)
            assert control.extractfile("./" + name).read().decode() == expected
            assert expected.startswith("#!/bin/bash\n") and "\r" not in expected
            assert control.getmember("./" + name).mode == 0o755
    with tarfile.open(fileobj=io.BytesIO(members["data.tar.gz"])) as tar:
        base = f"./usr/local/{app_id}/"
        for member in tar:
            assert member.name in ("./usr", "./usr/local", base.rstrip("/")) or member.name.startswith(base)
            assert ".." not in Path(member.name).parts and not member.issym() and not member.islnk()
        get = lambda name: tar.extractfile(base + name).read()
        assert json.loads(get("config.ini")) == tos_config(config, arch)
        assert get(f"{app_id}.lang").decode() == language_file(config)
        assert get(f"images/icons/{app_id}.svg") == (ROOT / "packaging/icon.svg").read_bytes()
        binary = get(f"bin/{app_id}")
        assert binary[:5] == b"\x7fELF\x02"
        assert struct.unpack_from("<H", binary, 18)[0] == {"amd64": 62, "arm64": 183}[arch]
        offset = struct.unpack_from("<Q", binary, 32)[0]
        size, count = struct.unpack_from("<HH", binary, 54)
        assert all(struct.unpack_from("<I", binary, offset + index * size)[0] != 3 for index in range(count)), "dynamic ELF loader"
        assert tar.getmember(base + f"bin/{app_id}").mode == 0o755
        assert json.loads(get("build-info.json")) == {
            "id": app_id, "version": config["version"], "go": "go" + (ROOT / ".go-version").read_text().strip(),
            "os": "linux", "arch": arch, "binarySha256": hashlib.sha256(binary).hexdigest(),
        }
        for kind in ("service", "socket"):
            assert get(f"init.d/{app_id}.{kind}").decode() == (ROOT / "packaging" / f"{kind}.in").read_text().replace("@APP_ID@", app_id)
        service = get(f"init.d/{app_id}.service").decode()
        start_args = next(line for line in service.splitlines() if line.startswith("ExecStart=")).split()[1:]
        assert not any(arg.startswith("-") and arg.lstrip("-").split("=", 1)[0] in {"web", "dev"} for arg in start_args), "production unit enables development static serving"
        with tarfile.open(fileobj=io.BytesIO(get("webui.bz2")), mode="r:bz2") as web:
            files = {item.name: web.extractfile(item).read() for item in web if item.isfile()}
            assert files == {str(item.relative_to(ROOT / "dist")): item.read_bytes() for item in (ROOT / "dist").rglob("*") if item.is_file()}
            html = files["index.html"].decode()
            assert f'name="tos-app-version" content="{config["version"]}"' in html
            assert not re.search(r'(src|href)=["\']https?://', html)
            icon = re.search(r'<link[^>]+rel="icon"[^>]+href="\./([^\"]+)"', html)[1]
            assert files[icon] == (ROOT / "packaging/icon.svg").read_bytes()
        for name in ("tos-app-core", "tos-app-ui-kit", "vue"):
            assert get(f"licenses/{name}.txt") == (ROOT / "node_modules" / name / "LICENSE").read_bytes()
        assert get("licenses/lucide-vue.txt") == (ROOT / "node_modules/@lucide/vue/LICENSE").read_bytes()
        for name in ("go", "x-sys", "tos-app-core-go"):
            assert get(f"licenses/{name}.txt")
    print(f"PASS {package.name}: manifest, static {arch} ELF, frontend, socket, scripts, licenses")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("packages", type=Path, nargs="+")
    args = parser.parse_args()
    for package in args.packages:
        verify(package)
