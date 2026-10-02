#!/usr/bin/env python3
"""Focused regression cases for the documentation graph checker."""

import tempfile
import unittest
from pathlib import Path

from importlib.machinery import SourceFileLoader

checker = SourceFileLoader("check_docs", str(Path(__file__).with_name("check-docs.py"))).load_module()


class DocumentationCheckTest(unittest.TestCase):
    def test_links_fragments_and_reachability(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs").mkdir()
            (root / "docs/README.md").write_text(
                "# Index\n[Page](page.md#same-1)\n"
                "[Variable](page.md#rusui_worker_secret)\n"
                "[Emphasis](page.md#emphasis)\n"
                "[Source](../source)\n[Web](https://example.com/missing)\n"
                "```md\n[Example](missing-in-code.md)\n```\n"
            )
            (root / "source").mkdir()
            (root / "docs/page.md").write_text(
                "# Same\n# Same\n## `RUSUI_WORKER_SECRET`\n## _Emphasis_\n"
            )
            self.assertEqual([], checker.check(root, Path("docs/README.md")))
            (root / "docs/extra.md").write_text("# Extra\n")
            self.assertIn("unreachable page docs/extra.md", checker.check(root, Path("docs/README.md")))
            (root / "docs/README.md").write_text(
                "# Index\n[Page](page.md#missing)\n"
                "[Bad](absent.md)\n![Image](gone.png)\n"
            )
            errors = checker.check(root, Path("docs/README.md"))
            self.assertTrue(any("missing fragment" in error for error in errors))
            self.assertTrue(any("missing target" in error for error in errors))
            self.assertEqual(2, sum("missing target" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
