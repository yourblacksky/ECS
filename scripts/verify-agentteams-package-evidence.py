#!/usr/bin/env python3
"""Reject stale or incomplete AgentTeams evidence before packaging."""

import json
from pathlib import Path
import sys


def main() -> None:
    if len(sys.argv) != 3:
        raise SystemExit(
            "usage: verify-agentteams-package-evidence.py EVIDENCE_DIR EXPECTED_REVISION"
        )

    root = Path(sys.argv[1])
    expected_revision = sys.argv[2]
    stages = ("scope", "extract", "verify", "dispatch")
    required = [root / "summary.json", root / "work-order.json", root / "SHA256SUMS"]
    for stage in stages:
        prefix = f"agentteams-office-e2e-{stage}"
        required.extend(
            [
                root / f"{prefix}-meta.json",
                root / f"{prefix}-result.json",
                root / f"{prefix}-result.md",
                root / f"execution-trace-{stage}.json",
            ]
        )
    missing = [path.name for path in required if not path.is_file() or path.stat().st_size == 0]
    if missing:
        raise SystemExit("AgentTeams package evidence is incomplete: " + ", ".join(missing))

    summary = json.loads((root / "summary.json").read_text())
    expected_tasks = [f"agentteams-office-e2e-{stage}" for stage in stages]
    checks = summary.get("checks") or {}
    if summary.get("source_revision") != expected_revision:
        raise SystemExit(
            "AgentTeams evidence revision mismatch: "
            f"{summary.get('source_revision')} != {expected_revision}"
        )
    if summary.get("task_ids") != expected_tasks:
        raise SystemExit("AgentTeams evidence does not contain the four ordered stage tasks")
    if not checks or not all(value is True for value in checks.values()):
        raise SystemExit("AgentTeams evidence contains failed or missing checks")

    print("AgentTeams package evidence matches the current four-stage revision.")


if __name__ == "__main__":
    main()
