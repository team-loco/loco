#!/usr/bin/env python3
import pathlib
import sys

import yaml

LEVELS = {"none": 0, "read": 1, "write": 2}
WORKFLOW_DIR = pathlib.Path(".github/workflows")
UNDECLARED = "read"


def normalise(perms):
    if perms is None:
        return None
    if isinstance(perms, str):
        level = {"read-all": "read", "write-all": "write"}.get(perms)
        if level is None:
            return {}
        return {"*": level}
    return {k: v for k, v in perms.items()}


def required(doc):
    top = normalise(doc.get("permissions"))
    merged = {}
    for job in (doc.get("jobs") or {}).values():
        if not isinstance(job, dict):
            continue
        scopes = normalise(job.get("permissions"))
        if scopes is None:
            scopes = top or {}
        for scope, level in scopes.items():
            if LEVELS.get(level, 0) > LEVELS.get(merged.get(scope, "none"), 0):
                merged[scope] = level
    return merged


def main():
    docs = {}
    for path in sorted(WORKFLOW_DIR.glob("*.y*ml")):
        docs[path.name] = yaml.safe_load(path.read_text())

    failures = []
    for name, doc in docs.items():
        top = normalise(doc.get("permissions"))
        for job_id, job in (doc.get("jobs") or {}).items():
            if not isinstance(job, dict):
                continue
            uses = job.get("uses")
            if not isinstance(uses, str) or not uses.startswith("./.github/workflows/"):
                continue
            callee = uses.split("/")[-1].split("@")[0]
            if callee not in docs:
                failures.append(f"{name}: job '{job_id}' calls {callee}, which does not exist")
                continue
            granted = normalise(job.get("permissions"))
            if granted is None:
                granted = top
            if granted is None:
                granted = {"*": UNDECLARED}
            need = required(docs[callee])
            for scope, level in sorted(need.items()):
                have = granted.get(scope, granted.get("*", "none"))
                if LEVELS.get(have, 0) < LEVELS.get(level, 0):
                    failures.append(
                        f"{name}: job '{job_id}' grants {scope}: {have}, "
                        f"but {callee} requires {scope}: {level}"
                    )

    if failures:
        print("Reusable workflow permission mismatches:\n")
        for f in failures:
            print(f"  {f}")
        print(
            "\nA called workflow cannot request more permission than its caller grants; "
            "GitHub fails the whole run at startup."
        )
        return 1

    print(f"Checked {len(docs)} workflows: all reusable-workflow call sites grant enough permission.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
