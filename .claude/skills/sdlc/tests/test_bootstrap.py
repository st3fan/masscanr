"""Regression tests for the non-destructive SDLC bootstrap updater."""

from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "bootstrap.py"
spec = importlib.util.spec_from_file_location("sdlc_bootstrap", SCRIPT)
assert spec is not None and spec.loader is not None
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)

START = bootstrap.START
END = bootstrap.END


class BootstrapTests(unittest.TestCase):
    def test_new_project_creates_both_guides(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.assertTrue(bootstrap.run(root))
            for name in bootstrap.FILES:
                content = (root / name).read_text()
                self.assertIn(START, content)
                self.assertIn(END, content)
            self.assertFalse(bootstrap.run(root))

    def test_existing_custom_text_survives_template_refresh(self) -> None:
        old = f"old header\n{START}\nold rules\n{END}\ncustom safety rule\n"
        updated = bootstrap.reconcile(old, f"{START}\nnew rules\n{END}\n")
        self.assertEqual(updated, f"old header\n{START}\nnew rules\n{END}\ncustom safety rule\n")
        self.assertEqual(bootstrap.reconcile(updated, f"{START}\nnew rules\n{END}\n"), updated)

    def test_unmanaged_document_is_preserved_and_extended(self) -> None:
        old = "# AGENTS.md\n\nDo not run production scans.\n"
        result = bootstrap.reconcile(old, f"{START}\nSDLC rules\n{END}\n")
        self.assertTrue(result.startswith(old))
        self.assertIn("Do not run production scans.", result)
        self.assertEqual(result.count(START), 1)

    def test_invalid_markers_are_rejected(self) -> None:
        template = f"{START}\nSDLC rules\n{END}"
        for old in (f"custom {START}", f"{END} before {START}", f"{START} {END} {START}"):
            with self.subTest(old=old):
                with self.assertRaises(ValueError):
                    bootstrap.reconcile(old, template)

    def test_dry_run_and_check_do_not_write(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.assertTrue(bootstrap.run(root, dry_run=True))
            self.assertTrue(bootstrap.run(root, check=True))
            self.assertFalse((root / "AGENTS.md").exists())
            self.assertFalse((root / "HUMANS.md").exists())

    def test_invalid_existing_document_prevents_partial_update(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            agents = root / "AGENTS.md"
            agents.write_text(f"{START}\nmissing end", encoding="utf-8")
            with self.assertRaises(ValueError):
                bootstrap.run(root)
            self.assertFalse((root / "HUMANS.md").exists())
            self.assertEqual(agents.read_text(), f"{START}\nmissing end")

    def test_refuse_symlink_targets(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            outside = root / "elsewhere.md"
            outside.write_text("keep", encoding="utf-8")
            try:
                (root / "AGENTS.md").symlink_to(outside)
            except (OSError, NotImplementedError):
                self.skipTest("Symlink creation unavailable")
            with self.assertRaises(ValueError):
                bootstrap.run(root)
            self.assertEqual(outside.read_text(), "keep")


if __name__ == "__main__":
    unittest.main()
