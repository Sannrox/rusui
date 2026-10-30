#!/usr/bin/env python3
"""Write a CycloneDX 1.5 SBOM from `go list -m -json all`."""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

MAIN_MODULE = "github.com/sannrox/rusui"


def decode_json_stream(raw: str) -> list[dict]:
    decoder = json.JSONDecoder()
    idx = 0
    items: list[dict] = []
    while idx < len(raw):
        while idx < len(raw) and raw[idx].isspace():
            idx += 1
        if idx >= len(raw):
            break
        obj, end = decoder.raw_decode(raw, idx)
        items.append(obj)
        idx = end
    return items


def go_list_modules(root: Path) -> list[dict]:
    raw = subprocess.check_output(
        ["go", "list", "-mod=readonly", "-m", "-json", "all"],
        cwd=root,
        text=True,
    )
    return decode_json_stream(raw)


def bom_from_modules(modules: list[dict], version: str) -> dict:
    components = []
    main = None
    for module in modules:
        path = module.get("Path") or ""
        if module.get("Main"):
            main = module
            continue
        replaced = module.get("Replace") or {}
        if replaced.get("Path") and not replaced.get("Version"):
            continue
        name = replaced.get("Path") or path
        mod_version = replaced.get("Version") or module.get("Version") or ""
        if not name or not mod_version:
            continue
        purl = f"pkg:golang/{name}@{mod_version}"
        components.append(
            {
                "type": "library",
                "bom-ref": purl,
                "name": name,
                "version": mod_version,
                "purl": purl,
            }
        )
    if main is None:
        raise SystemExit("go list did not include the main module")
    root_name = main.get("Path") or MAIN_MODULE
    root_purl = f"pkg:golang/{root_name}@v{version}"
    return {
        "bomFormat": "CycloneDX",
        "specVersion": "1.5",
        "version": 1,
        "metadata": {
            "component": {
                "type": "application",
                "bom-ref": root_purl,
                "name": "rusui",
                "version": version,
                "purl": root_purl,
            }
        },
        "components": sorted(components, key=lambda item: item["bom-ref"]),
    }


def workspace_version(root: Path) -> str:
    text = (root / "VERSION").read_text().strip()
    if not text:
        raise SystemExit("VERSION is empty")
    return text


def self_test() -> None:
    modules = [
        {"Path": MAIN_MODULE, "Main": True, "Version": "(devel)"},
        {"Path": "golang.org/x/sync", "Version": "v0.8.0"},
        {
            "Path": "example.com/old",
            "Version": "v1.0.0",
            "Replace": {"Path": "../local"},
        },
    ]
    bom = bom_from_modules(modules, "0.1.0")
    assert bom["bomFormat"] == "CycloneDX"
    assert bom["metadata"]["component"]["version"] == "0.1.0"
    assert bom["metadata"]["component"]["type"] == "application"
    assert len(bom["components"]) == 1
    assert bom["components"][0]["name"] == "golang.org/x/sync"
    print("release-sbom self-test passed")


def main(argv: list[str]) -> None:
    if argv == ["--self-test"]:
        self_test()
        return
    if len(argv) != 1:
        raise SystemExit("usage: release-sbom.py OUT")
    root = Path(__file__).resolve().parent.parent
    bom = bom_from_modules(go_list_modules(root), workspace_version(root))
    out = Path(argv[0])
    out.write_text(json.dumps(bom, indent=2) + "\n")
    print(out)


if __name__ == "__main__":
    main(sys.argv[1:])
