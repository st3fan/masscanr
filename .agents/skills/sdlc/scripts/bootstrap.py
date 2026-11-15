#!/usr/bin/env python3
"""Create or refresh the project-facing guides managed by the sdlc skill."""

from __future__ import annotations

import argparse
from pathlib import Path
import sys

START = "<!-- sdlc:managed:start -->"
END = "<!-- sdlc:managed:end -->"
FILES = ("AGENTS.md", "HUMANS.md")


def managed_block(text: str) -> str:
    if text.count(START) != 1 or text.count(END) != 1:
        raise ValueError("Expected exactly one pair of SDLC management markers")
    start = text.index(START)
    end = text.index(END)
    if start >= end:
        raise ValueError("SDLC management markers are out of order")
    return text[start : end + len(END)]


def reconcile(existing: str | None, template: str) -> str:
    block = managed_block(template)
    if existing is None:
        return template

    starts, ends = existing.count(START), existing.count(END)
    if starts == 0 and ends == 0:
        return existing.rstrip("\n") + "\n\n" + block + "\n"
    if starts != 1 or ends != 1:
        raise ValueError("Existing document has incomplete or duplicate SDLC markers")

    start = existing.index(START)
    end = existing.index(END)
    if start >= end:
        raise ValueError("Existing SDLC management markers are out of order")
    return existing[:start] + block + existing[end + len(END) :]


def run(root: Path, *, dry_run: bool = False, check: bool = False) -> bool:
    if not root.is_dir():
        raise ValueError(f"Not a directory: {root}")

    assets = Path(__file__).resolve().parents[1] / "assets"
    changes: list[tuple[Path, str, str]] = []

    for filename in FILES:
        target = root / filename
        if target.is_symlink():
            raise ValueError(f"Refusing to edit a symlink: {target}")
        template = (assets / filename).read_text(encoding="utf-8")
        existing = target.read_text(encoding="utf-8") if target.exists() else None
        updated = reconcile(existing, template)
        if updated != existing:
            changes.append((target, updated, "create" if existing is None else "update"))
        else:
            print(f"unchanged: {target}")

    for path, content, action in changes:
        print(f"{'would ' if dry_run or check else ''}{action}: {path}")
        if not (dry_run or check):
            path.write_text(content, encoding="utf-8")

    return bool(changes)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="Target project root")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--dry-run", action="store_true", help="Show changes without writing")
    mode.add_argument("--check", action="store_true", help="Exit 1 if templates are out of date")
    args = parser.parse_args()

    try:
        changed = run(args.root.resolve(), dry_run=args.dry_run, check=args.check)
    except (OSError, UnicodeError, ValueError) as error:
        print(f"sdlc bootstrap: {error}", file=sys.stderr)
        return 2
    return int(changed) if args.check else 0


if __name__ == "__main__":
    raise SystemExit(main())
