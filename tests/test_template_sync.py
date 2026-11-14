"""Exercise the Action's merge/reset sequence with unrelated app histories."""
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent


def git(root, *args):
    result = subprocess.run(
        ["git", "-c", "commit.gpgsign=false", "-C", str(root), *args],
        text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
    )
    if result.returncode:
        raise RuntimeError(f"git {' '.join(args)} failed:\n{result.stdout}")
    return result.stdout


def write(root, files):
    for name, content in files.items():
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)


def commit(root):
    git(root, "add", ".")
    git(root, "commit", "-qm", "fixture update")


class TemplateSyncTests(unittest.TestCase):
    def test_sync_preserves_app_and_accepts_shared_updates(self):
        rules = (ROOT / ".templatesyncignore").read_text()
        with tempfile.TemporaryDirectory(prefix="tos-template-sync-") as temp:
            source, target = (Path(temp) / name for name in ("source", "target"))
            app_files = {
                "app.json": "custom identity",
                "package.json": "custom dependencies",
                "package-lock.json": "custom npm lock",
                "go.mod": "custom module",
                "go.sum": "custom Go lock",
                "src/App.vue": "custom UI",
                "server/app.go": "custom API",
                "packaging/icon.svg": "custom icon",
                "README.md": "custom documentation",
                ".github/workflows/release.yml": "custom CI",
                "scripts/business.py": "app-only script",
            }
            shared_files = {
                "scripts/build-package.py": "old packaging",
                "packaging/service.in": "old service",
                "packaging/socket.in": "old socket",
                "packaging/debian/postinst.in": "old installer",
                ".go-version": "old toolchain",
            }
            for root in (source, target):
                root.mkdir()
                git(root, "init", "-q", "-b", "main")
                git(root, "config", "user.name", "Template Sync Test")
                git(root, "config", "user.email", "test@app.test")
                git(root, "config", "pull.rebase", "false")
                write(root, {".templatesyncignore": rules, **shared_files})
            write(source, {name: "template sample" for name in app_files if name != "scripts/business.py"})
            write(target, app_files)
            commit(source)
            commit(target)

            for version in ("first", "second"):
                with self.subTest(version=version):
                    updates = {name: version for name in shared_files}
                    updates["scripts/new-check.py"] = version
                    write(source, {
                        **updates,
                        "src/new-sample.vue": "template-only UI",
                        "future-default.txt": "unknown template addition",
                        # The Action restores the target's tracked policy before filtering.
                        ".templatesyncignore": ":!src/**\n*\n",
                    })
                    commit(source)
                    git(target, "pull", str(source), "main", "--allow-unrelated-histories",
                        "--squash", "--strategy=recursive", "-X", "theirs")
                    git(target, "reset", "--", ".templatesyncignore")
                    git(target, "checkout", "--", ".templatesyncignore")
                    git(target, "add", ".")
                    patterns = shlex.split((target / ".templatesyncignore").read_text(), comments=True)
                    git(target, "reset", "--", *patterns)
                    git(target, "clean", "-df")
                    git(target, "checkout", "--", ".")

                    changed = set(git(target, "diff", "--cached", "--name-only").splitlines())
                    self.assertEqual(changed, set(updates))
                    for name, content in {**app_files, **updates, ".templatesyncignore": rules}.items():
                        self.assertEqual((target / name).read_text(), content, name)
                    self.assertFalse((target / "src/new-sample.vue").exists())
                    self.assertFalse((target / "future-default.txt").exists())
                    git(target, "commit", "-qm", "sync tooling")


if __name__ == "__main__":
    unittest.main()
