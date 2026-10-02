#!/usr/bin/env python3
"""Check the repository documentation graph and local Markdown links."""

from __future__ import annotations

import argparse
import html
import re
import sys
import unicodedata
from collections import deque
from pathlib import Path
from urllib.parse import unquote, urlsplit


LINK = re.compile(r"!?\[[^\]]+\]\((<[^>]+>|[^\s)]+)(?:\s+[^)]*)?\)")
REFERENCE = re.compile(r"^\s{0,3}\[[^\]]+\]:\s*(<[^>]+>|\S+)", re.MULTILINE)
HEADING = re.compile(r"^#{1,6}\s+(.+?)\s*#*\s*$")
HTML_ID = re.compile(r"\b(?:id|name)=[\"']([^\"']+)[\"']")


def pages(root: Path) -> set[Path]:
    files = set(root.glob("*.md"))
    for directory in ("docs", "build", "eval"):
        files.update((root / directory).rglob("*.md"))
    return {path.resolve() for path in files}


def visible_lines(source: str):
    fence = None
    for line_number, line in enumerate(source.splitlines(), 1):
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            character, length = marker.group(1)[0], len(marker.group(1))
            if fence is None:
                fence = (character, length)
            elif fence[0] == character and length >= fence[1]:
                fence = None
            continue
        if fence is None:
            yield line_number, line


def slug(label: str) -> str:
    label = re.sub(r"\[([^]]+)\]\([^)]*\)", r"\1", label)
    label = re.sub(r"<[^>]+>", "", label)
    label = html.unescape(label)
    label = re.sub(r"(?<!\w)_{1,2}(.+?)_{1,2}(?!\w)", r"\1", label)
    label = re.sub(r"[`*~]", "", label).lower()
    label = "".join(character for character in label if
                    character in "-_ " or character.isalnum() or
                    unicodedata.category(character).startswith("M"))
    return re.sub(r"\s", "-", label.strip())


def anchors(source: str) -> set[str]:
    result = set()
    seen = {}
    for _, line in visible_lines(source):
        match = HEADING.match(line)
        if match:
            base = slug(match.group(1))
            count = seen.get(base, 0)
            seen[base] = count + 1
            result.add(f"{base}-{count}" if count else base)
        result.update(HTML_ID.findall(line))
    return result


def destinations(source: str):
    for number, line in visible_lines(source):
        for match in LINK.finditer(line):
            yield number, match.group(1).strip("<>")
        for match in REFERENCE.finditer(line):
            yield number, match.group(1).strip("<>")


def check(root: Path, entry: Path) -> list[str]:
    root = root.resolve()
    entry = (root / entry).resolve()
    documents = pages(root)
    errors = []
    graph = {path: set() for path in documents}
    anchor_map = {path: anchors(path.read_text(encoding="utf-8")) for path in documents}
    for page in sorted(documents):
        for number, destination in destinations(page.read_text(encoding="utf-8")):
            parsed = urlsplit(destination)
            if parsed.scheme or parsed.netloc or destination.startswith("mailto:"):
                continue
            target = (page.parent / unquote(parsed.path)).resolve() if parsed.path else page
            location = f"{page.relative_to(root)}:{number}"
            if not target.is_relative_to(root) or not target.exists():
                errors.append(f"{location}: missing target {destination}")
                continue
            if target.is_dir():
                index = target / "README.md"
                if index.exists():
                    target = index
            if parsed.fragment and target.suffix == ".md" and target in anchor_map:
                fragment = unquote(parsed.fragment)
                if fragment not in anchor_map[target]:
                    errors.append(f"{location}: missing fragment {destination}")
            if target in documents:
                graph[page].add(target)
    if entry not in documents:
        errors.append(f"missing entry {entry.relative_to(root)}")
        return errors
    reached = {entry}
    queue = deque([entry])
    while queue:
        for target in graph[queue.popleft()]:
            if target not in reached:
                reached.add(target)
                queue.append(target)
    for page in sorted(documents - reached):
        errors.append(f"unreachable page {page.relative_to(root)}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--entry", type=Path, default=Path("docs/README.md"))
    args = parser.parse_args()
    errors = check(args.root, args.entry)
    for error in errors:
        print(error, file=sys.stderr)
    if errors:
        print(f"documentation check: {len(errors)} error(s)", file=sys.stderr)
        return 1
    print(f"documentation check: {len(pages(args.root.resolve()))} pages reachable; links and fragments valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
