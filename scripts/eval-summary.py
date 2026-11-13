#!/usr/bin/env python3
"""Report repeated Go evaluation results, without prompts or credentials."""
import collections
import json
import sys

results = collections.defaultdict(collections.Counter)
with open(sys.argv[1], encoding="utf-8") as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("Test") and event.get("Action") in {"pass", "fail", "skip"}:
            results[(event["Package"], event["Test"])][event["Action"]] += 1
print("Native harness: Codex 0.164.0. Offline transport fixtures; no paid model inference.")
print("\n| Case | Passed / executed | Skipped |\n| --- | --- | --- |")
for (package, case), counts in sorted(results.items()):
    if any(word in case for word in ("Injection", "Merge", "Approval", "Policy", "Handoff", "Route", "Native", "Sanit", "Registry")):
        total = counts["pass"] + counts["fail"]
        print(f"| {package.rsplit('/', 1)[-1]}/{case} | {counts['pass']}/{total} | {counts['skip']} |")
