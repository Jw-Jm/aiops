#!/usr/bin/env python3
"""Reject skipped or empty suites at explicit acceptance entry points."""
import json
import sys

if len(sys.argv) == 3 and sys.argv[1] == "--playwright":
    with open(sys.argv[2], encoding="utf-8") as report:
        data = json.load(report)
    stats = data.get("stats", {})
    if (stats.get("expected", 0) <= 0 or any(stats.get(key, 0) for key in ("skipped", "unexpected", "flaky"))
            or data.get("errors")):
        print("Acceptance suite did not fully execute: browser failures, skips or no tests passed", file=sys.stderr)
        sys.exit(1)
    print(f"Browser acceptance report: {stats['expected']} tests passed; zero skips.")
    sys.exit(0)

passed = 0
skipped = []
with open(sys.argv[1], encoding="utf-8") as report:
    for line in report:
        event = json.loads(line)
        if event.get("Action") == "skip":
            skipped.append(event.get("Test", event.get("Package", "unknown")))
        if event.get("Action") == "pass" and event.get("Test"):
            passed += 1
if skipped or not passed:
    print("Acceptance suite did not fully execute: " + ", ".join(skipped or ["no tests passed"]), file=sys.stderr)
    sys.exit(1)
print(f"Acceptance report: {passed} tests passed; zero skips.")
