"""Only guard.py may touch the anki library, so no tool can bypass the ownership checks."""

from __future__ import annotations

import ast
from pathlib import Path

SRC = Path(__file__).resolve().parent.parent / "src" / "anki_mcp"


def test_only_guard_imports_anki():
    offenders = []
    for path in SRC.glob("*.py"):
        if path.name == "guard.py":
            continue
        for node in ast.walk(ast.parse(path.read_text())):
            names = []
            if isinstance(node, ast.Import):
                names = [a.name for a in node.names]
            elif isinstance(node, ast.ImportFrom) and node.module:
                names = [node.module]
            offenders += [f"{path.name}: {n}" for n in names if n == "anki" or n.startswith("anki.")]
    assert not offenders, offenders


def test_tools_do_not_reach_the_raw_collection():
    text = (SRC / "server.py").read_text()
    assert "_raw_collection_for_tests" not in text and "._col" not in text
