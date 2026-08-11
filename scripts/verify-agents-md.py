#!/usr/bin/env python3
"""Cross-check AGENTS entry/reference claims against the tree they describe.

Deliberately asserts the numbers currently written in the doc, so a stale claim
shows up as a FAIL rather than being silently recomputed. Run from the repo root.
"""

import glob
import json
import pathlib
import re
import subprocess
import sys

DOC = "\n".join(
    pathlib.Path(path).read_text()
    for path in ("AGENTS.md", "AGENTS_REFERENCE.md")
)
fails = []


def check(label, claimed, actual):
    ok = claimed == actual
    print(f"  {'ok  ' if ok else 'FAIL'}  {label}: doc={claimed} actual={actual}")
    if not ok:
        fails.append(label)


def doc_has(snippet, label):
    ok = snippet in DOC
    print(f"  {'ok  ' if ok else 'FAIL'}  {label}")
    if not ok:
        fails.append(label)


print("counts")
tools = glob.glob("tools.d/*.json")
builtin = [f for f in tools if '"@builtin"' in pathlib.Path(f).read_text()]
device = [f for f in tools if '"@device"' in pathlib.Path(f).read_text()]
skills = {json.load(open(f))["name"]: json.load(open(f))
          for f in glob.glob("skills.d/*/skill.json")}
check("tools.d", 24, len(tools))
doc_has("`tools.d/` 已从 16 涨到 **24**", "verifier coverage tool-count prose")
check("@builtin", 18, len(builtin))
check("@device", 6, len(device))
check("skills.d", 23, len(skills))
check("agents.d", 15, len(glob.glob("agents.d/*.json")))
doc_has("`agents.d/` **15** 个", "agents.d inventory prose")
check("pipelines.d", 1, len(glob.glob("pipelines.d/*.json")))
check("office.d", 3, len(glob.glob("office.d/*.json")))
check("workflows", 5, len(glob.glob(".github/workflows/*.yml")))

print("\nregistration invariant  18 = 15 + 1 + 2")
toolbox = pathlib.Path("internal/toolbox/toolbox.go").read_text()
block = re.search(r"builtinHandlers = map.*?\n}", toolbox, re.S).group(0)
check("builtinHandlers map", 15, len(re.findall(r'^\t"', block, re.M)))

print("\ntoolchain")
ci = pathlib.Path(".github/workflows/ci.yml").read_text()
check("ci go-version", "1.25.12", re.search(r'go-version:\s*"([^"]+)"', ci).group(1))
check("go.mod", "1.25.0", re.search(r"^go (\S+)", pathlib.Path("go.mod").read_text(), re.M).group(1))
check("MOBILE_GO_TOOLCHAIN", "go1.25.12",
      re.search(r"MOBILE_GO_TOOLCHAIN \?= (\S+)", pathlib.Path("Makefile").read_text()).group(1))

print("\ntest-suite claims")
mob = pathlib.Path("mobile/mobile_test.go").read_text()
m = re.search(r"status\.Tools != (\d+) \|\| status\.Skills != (\d+)", mob)
check("TestMobileLifecycle Tools", 16, int(m.group(1)))
check("TestMobileLifecycle Skills", 15, int(m.group(2)))
skips = subprocess.run(
    ["git", "grep", "-l", "-F", "t.Skip", "--", "*_test.go"],
    capture_output=True,
    text=True,
    check=False,
).stdout.splitlines()
check("t.Skip files", 1, len(skips))

print("\nregistry claims")
allowed = json.load(open("agents.d/executor.json"))
check("allowed_tools minus allowed_skills", 8,
      len(set(allowed["allowed_tools"]) - set(allowed["allowed_skills"])))
check("skill names == allowed_skills", True,
      set(skills) == set(allowed["allowed_skills"]))
no_exec = [n for n, d in skills.items()
           if (d.get("agents") or {}).get("roles")
           and "executor" not in [r.lower() for r in d["agents"]["roles"]]]
check("skills invisible to default executor", 2, len(no_exec))

print("\ngrounding budget")
bodies = [len(re.sub(r"^---.*?---\n", "", pathlib.Path(f).read_text(), flags=re.S))
          for f in glob.glob("skills.d/*/SKILL.md")]
check("SKILL.md count", 23, len(bodies))
check("SKILL.md min rune", 638, min(bodies))
check("SKILL.md max rune", 1570, max(bodies))

print("\nreversal claims (prose, not numbers)")
agent_go = pathlib.Path("internal/agent/agent.go").read_text()
doc_has("Preserve an explicit stage role", "stage-role preservation quoted in doc")
check("stage role preserved in code", True,
      'if promptContext.Role == "" {' in agent_go)
check("single executor in default pipeline", ["executor"],
      json.load(open("pipelines.d/default.json"))["executors"])
helpers = pathlib.Path("internal/server/http_helpers.go").read_text()
check("withAdminAuth fails closed", True,
      "!tokenMatches(s.cfg.AdminToken, bearerToken(r))" in helpers)
check("withAuthScope still open-by-default", True,
      's.cfg.AuthToken == "" && s.network == nil' in helpers)

print()
if fails:
    print(f"{len(fails)} STALE CLAIM(S): {', '.join(fails)}")
    sys.exit(1)
print("all AGENTS entry/reference claims match the tree")
