#!/usr/bin/env python3
"""Thin Codex adapter; all decisions live in scripts/harness/core.py."""

from __future__ import annotations

import importlib.util
import pathlib
import subprocess
import sys


sys.dont_write_bytecode = True
root = pathlib.Path(
    subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip()
)
source = root / "scripts/harness/core.py"
spec = importlib.util.spec_from_file_location("ecs_harness_core", source)
if spec is None or spec.loader is None:
    print("ECS Harness cannot load shared core", file=sys.stderr)
    raise SystemExit(2)
module = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = module
spec.loader.exec_module(module)
raise SystemExit(module.pre_tool_use_main())
