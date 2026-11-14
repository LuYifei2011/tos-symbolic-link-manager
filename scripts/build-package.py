#!/usr/bin/env python3
"""Build a self-contained TOS deb from a validated application manifest."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
from metadata import ROOT, check, language_file, tos_config
from toolchain import release_go


def archive(directory, excludes=()):
    data = io.BytesIO()
    # The official TOS package checker requires GNU tar for the deb members.
    with tarfile.open(fileobj=data, mode="w:gz", format=tarfile.GNU_FORMAT) as tar:
        for item in sorted(directory.rglob("*")):
            relative = item.relative_to(directory)
            if relative.parts[0] in excludes:
                continue
            info = tar.gettarinfo(str(item), "./" + str(relative))
            info.uid = info.gid = 0
            info.uname = info.gname = "root"
            with item.open("rb") if item.is_file() else io.BytesIO() as stream:
                tar.addfile(info, stream if item.is_file() else None)
    return data.getvalue()


def render(source, target, app_id):
    target.write_text(source.read_text().replace("@APP_ID@", app_id))


def build(arch, go="go", release=False):
    config = check(release=release)
    app_id, version = config["id"], config["version"]
    env, go_version = release_go(go)
    env.update(GOOS="linux", GOARCH=arch, CGO_ENABLED="0")
    subprocess.run(["npm", "run", "build"], cwd=ROOT, check=True)
    stage = ROOT / "build" / f"stage-{arch}"
    if stage.exists():
        shutil.rmtree(stage)
    app = stage / "usr/local" / app_id
    (app / "bin").mkdir(parents=True)
    binary = app / "bin" / app_id
    subprocess.run([go, "build", "-trimpath", "-ldflags=-s -w", "-o", str(binary), "./server"], cwd=ROOT, env=env, check=True)
    binary.chmod(0o755)
    (app / "build-info.json").write_text(json.dumps({
        "id": app_id, "version": version, "go": go_version, "os": "linux", "arch": arch,
        "binarySha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
    }, indent=2) + "\n")
    (app / "config.ini").write_text(json.dumps(tos_config(config, arch), ensure_ascii=False, indent=2) + "\n")
    (app / f"{app_id}.lang").write_text(language_file(config))
    (app / "init.d").mkdir()
    for kind in ("service", "socket"):
        render(ROOT / "packaging" / f"{kind}.in", app / "init.d" / f"{app_id}.{kind}", app_id)
    (app / "images/icons").mkdir(parents=True)
    shutil.copy(ROOT / "packaging/icon.svg", app / "images/icons" / f"{app_id}.svg")
    with tarfile.open(app / "webui.bz2", "w:bz2", format=tarfile.USTAR_FORMAT) as tar:
        for item in sorted((ROOT / "dist").rglob("*")):
            tar.add(item, arcname=str(item.relative_to(ROOT / "dist")), recursive=False)
    for name in ("README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"):
        shutil.copy(ROOT / name, app)
    shutil.copytree(ROOT / "docs", app / "docs")
    licenses = app / "licenses"
    licenses.mkdir()
    for module, filename in (("github.com/LuYifei2011/tos-app-kit/go", "tos-app-core-go.txt"), ("golang.org/x/sys", "x-sys.txt")):
        metadata = json.loads(subprocess.check_output([go, "list", "-m", "-json", module], cwd=ROOT, env=env, text=True))
        shutil.copy(Path(metadata["Dir"]) / "LICENSE", licenses / filename)
    for name in ("tos-app-core", "tos-app-ui-kit", "@lucide/vue", "vue", "@vue/shared", "@vue/reactivity", "@vue/runtime-core", "@vue/runtime-dom"):
        shutil.copy(ROOT / "node_modules" / name / "LICENSE", licenses / (name.replace("/", "-").replace("@", "") + ".txt"))
    goroot = Path(subprocess.check_output([go, "env", "GOROOT"], env=env, text=True).strip())
    candidates = [goroot / "LICENSE"] + ([goroot.parent / "LICENSE"] if goroot.name == "libexec" else [])
    license_file = next((item for item in candidates if item.is_file()), None)
    if license_file is None or "The Go Authors" not in license_file.read_text():
        raise ValueError("cannot find the selected Go toolchain LICENSE")
    shutil.copy(license_file, licenses / "go.txt")
    control = stage / "DEBIAN"
    control.mkdir()
    (control / "control").write_text(f"""Package: {app_id}
Version: {version}
Section: utils
Priority: optional
Architecture: {arch}
Maintainer: {config['publisher']} <{config['email']}>
Homepage: {config['help']}
Depends: systemd
Description: {config['description']['en-US']}
""")
    (control / "conffiles").write_text("")
    for name in ("postinst", "prerm", "postrm"):
        render(ROOT / "packaging/debian" / f"{name}.in", control / name, app_id)
        (control / name).chmod(0o755)
    for path in stage.rglob("*"):
        if path.is_file() and (path.parent == control or path.suffix in {".ini", ".lang", ".service", ".socket"}):
            path.write_bytes(path.read_bytes().replace(b"\r\n", b"\n"))
    output = ROOT / "build" / f"{app_id}_{version}_{arch}.deb"
    with output.open("wb") as stream:
        stream.write(b"!<arch>\n")
        for name, data in (("debian-binary", b"2.0\n"), ("control.tar.gz", archive(control)), ("data.tar.gz", archive(stage, ("DEBIAN",)))):
            stream.write(f"{name+'/':<16}{0:<12}{0:<6}{0:<6}{'100644':<8}{len(data):<10}`\n".encode("ascii"))
            stream.write(data)
            if len(data) % 2:
                stream.write(b"\n")
    print(output)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("arch", choices=("amd64", "arm64"))
    parser.add_argument("--go", default="go")
    parser.add_argument("--release", action="store_true", help="reject sample identity and placeholder icon")
    args = parser.parse_args()
    build(args.arch, args.go, args.release)
