#!/usr/bin/env python3
"""Preview explicitly reviewed channel affinity modes; never contacts a server."""
import argparse
import copy
import json
import os
from pathlib import Path

MODES = {"off", "prefer", "strict", "inherit"}


def plan_rules(rules, decisions):
    if not isinstance(rules, list) or any(not isinstance(r, dict) for r in rules):
        raise ValueError("Input must be a JSON array of affinity rules")
    names = [r.get("name") for r in rules]
    if any(not isinstance(n, str) or not n.strip() for n in names) or len(set(names)) != len(names):
        raise ValueError("Each rule requires a unique nonempty name")
    if any(name not in names or mode not in MODES for name, mode in decisions.items()):
        raise ValueError("Decisions must name existing rules and use off/prefer/strict/inherit")
    result, report, unresolved = copy.deepcopy(rules), [], []
    for rule in result:
        name, existing = rule["name"], rule.get("session_mode")
        if existing and existing not in MODES:
            raise ValueError("Unknown existing session mode")
        selected = decisions.get(name, existing)
        if not selected:
            unresolved.append(name)
        else:
            rule["session_mode"] = selected
            # An explicit mode has precedence. Keep the legacy flag consistent
            # for prefer/strict/off; inherit still depends on the global mode.
            if selected != "inherit":
                rule["skip_retry_on_failure"] = selected == "strict"
        report.append({"name": name, "before": existing or "legacy",
                       "legacy_skip_retry": bool(rules[names.index(name)].get("skip_retry_on_failure")),
                       "after": selected or "REVIEW_REQUIRED"})
    return result, report, unresolved


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("rules", type=Path, help="Exported channel_affinity_setting.rules JSON array")
    parser.add_argument("--decision", action="append", default=[], metavar="NAME=MODE")
    parser.add_argument("--output", type=Path, help="Write a new reviewed rules file (no API/database writes)")
    args = parser.parse_args()
    try:
        decisions = {}
        for entry in args.decision:
            name, mode = entry.rsplit("=", 1)
            if name in decisions:
                raise ValueError("Duplicate decision")
            decisions[name] = mode
        updated, report, unresolved = plan_rules(json.loads(args.rules.read_text()), decisions)
        print(json.dumps({"rules": report, "unresolved": unresolved}, ensure_ascii=False, indent=2))
        if args.output:
            if unresolved:
                raise ValueError("All legacy rules must be reviewed before writing output")
            # Refuse overwrite: the input and rollback copy must remain intact.
            fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as stream:
                json.dump(updated, stream, ensure_ascii=False, indent=2)
                stream.write("\n")
        return 2 if unresolved else 0
    except (ValueError, OSError):
        parser.exit(2, "Invalid input or output; use unique rule names, explicit modes and a new output path.\n")


if __name__ == "__main__":
    raise SystemExit(main())
