import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "scripts"))
from metadata import check, load, sync, validate, tos_config

spec = importlib.util.spec_from_file_location("create_app", ROOT / "scripts/create-app.py")
creator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(creator)


class TemplateTests(unittest.TestCase):
    def test_manifest(self):
        check()
        for invalid in ("tos-demo", "../escape", "-app", "abc;touch", "UPPER", "a" * 25, "last-", "root", "admin", "docker"):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                validate(dict(load(), id=invalid))

    def test_metadata_injection_and_types(self):
        for key, value in (("version", "01.0.0"), ("publisher", "Owner\nDepends: evil"), ("email", "a@b.com\nx"), ("help", "https://user:secret@example.com"), ("beta", "false"), ("name", {})):
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate(dict(load(), **{key: value}))

    def test_created_app_is_independent_and_renamed(self):
        with tempfile.TemporaryDirectory(prefix="tos-template-test-") as temp:
            target = Path(temp) / "different-app"
            config = dict(load(), id="different-app", publisher="Test Owner", email="owner@my-app.test", help="https://my-app.test/help")
            creator.create(target, config)
            self.assertFalse((target / "node_modules").exists())
            self.assertFalse((target / ".git").exists())
            self.assertFalse((target / "build").exists())
            self.assertEqual(check(target)["id"], "different-app")
            package = json.loads((target / "package.json").read_text())
            for dependency in ("tos-app-core", "tos-app-ui-kit"):
                spec = package["dependencies"][dependency]
                self.assertTrue(spec.startswith("https://github.com/LuYifei2011/tos-app-kit/releases/download/"))
            self.assertNotIn("replace ", (target / "go.mod").read_text())
            for arch, platform in (("amd64", "x86_64"), ("arm64", "aarch64")):
                metadata = tos_config(config, arch)
                self.assertEqual(metadata["path"], "/different-app/")
                self.assertEqual(metadata["user"], "different-app")
                self.assertEqual(metadata["platform"], platform)
                self.assertEqual(metadata["low_version"], "7.0")
            config["version"] = "2.3.4"
            (target / "app.json").write_text(json.dumps(config))
            sync(target)
            self.assertEqual(check(target, tag="v2.3.4")["version"], "2.3.4")
            with self.assertRaises(ValueError):
                check(target, tag="v0.1.0")

    def test_generator_preserves_existing_destination(self):
        with tempfile.TemporaryDirectory() as temp:
            target = Path(temp)
            (target / "keep").write_text("existing")
            with self.assertRaises(FileExistsError):
                creator.create(target, load())
            self.assertEqual((target / "keep").read_text(), "existing")

    def test_release_rejects_placeholders(self):
        with tempfile.TemporaryDirectory() as temp:
            target = Path(temp) / "placeholder"
            creator.create(target, dict(load(), id="sample-app", publisher="Your Name"))
            with self.assertRaises(ValueError):
                check(target, release=True)

    def test_installer_keeps_data_and_rejects_symlinks(self):
        with tempfile.TemporaryDirectory(prefix="tos-install-test-") as temp:
            root = Path(temp)
            app = root / "app"
            app.mkdir()
            (app / "data").mkdir()
            (app / "data/keep").write_text("user data")
            (app / "site").mkdir()
            (app / "site/index.html").write_text("old")
            (root / "web").mkdir()
            (root / "web/index.html").write_text("new")
            subprocess.run(["tar", "-cjf", str(app / "webui.bz2"), "-C", str(root / "web"), "."], check=True)
            fake_bin = root / "bin"
            fake_bin.mkdir()
            for name, script in (("systemctl", "exit 0"), ("id", "exit 1")):
                path = fake_bin / name
                path.write_text("#!/bin/sh\n" + script + "\n")
                path.chmod(0o755)
            import os
            env = dict(os.environ, PATH=str(fake_bin) + os.pathsep + os.environ["PATH"])
            source = (ROOT / "packaging/debian/postinst.in").read_text().replace("@APP_ID@", "test-app").replace("app=/usr/local/test-app", "app='" + str(app) + "'")
            installer = root / "postinst"
            installer.write_text(source)
            subprocess.run(["/bin/bash", str(installer), "configure"], check=True, env=env, capture_output=True)
            self.assertEqual((app / "site/index.html").read_text(), "new")
            self.assertEqual((app / "data/keep").read_text(), "user data")
            (app / "site/index.html").unlink()
            (app / "site").rmdir()
            (app / "site").symlink_to(root / "web", target_is_directory=True)
            result = subprocess.run(["/bin/bash", str(installer), "configure"], env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((root / "web/index.html").read_text(), "new")


if __name__ == "__main__":
    unittest.main()
