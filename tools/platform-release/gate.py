"""Fails unless the push runs of `ci.yml`, which also publish the images, allow a main commit to be released.

Usage: gate.py LATEST_TAG COMMIT, with an empty LATEST_TAG when no release exists.
"""

import json
import subprocess
import sys
import time

from release import run

GH_ATTEMPTS = 5


def gh(*args):
    for attempt in range(GH_ATTEMPTS):
        try:
            return run("gh", *args)
        except subprocess.CalledProcessError:
            if attempt == GH_ATTEMPTS - 1:
                raise
            time.sleep(2**attempt)


def blocking(commits, jobs):
    """Returns (sha, reason) for each commit whose `ci.yml` push run blocks releasing the last commit.

    commits lists (sha, run) oldest first; run is None when the commit has no run, else a dict with
    `databaseId` and `conclusion`. jobs(run id) returns {job name: conclusion}. A failed `Changes`
    job is cleared only by its own run succeeding. The unfinished jobs of a cancelled run clear like
    failed jobs.
    """
    blocked = []
    for index, (sha, ci) in enumerate(commits):
        released = index == len(commits) - 1
        if ci is None:
            if released:
                blocked.append((sha, "no run"))
            continue
        conclusion = ci["conclusion"] or "in progress"
        if conclusion == "success":
            continue
        if released or conclusion not in ("failure", "cancelled"):
            blocked.append((sha, conclusion))
            continue
        failed = {
            name
            for name, result in jobs(ci["databaseId"]).items()
            if result not in ("success", "skipped")
        }
        for _, later in commits[index + 1 :]:
            if not failed or "Changes" in failed:
                break
            if later is not None:
                failed -= {
                    name
                    for name, result in jobs(later["databaseId"]).items()
                    if result == "success"
                }
        if failed:
            blocked.append((sha, conclusion + " (" + ", ".join(sorted(failed)) + ")"))
    return blocked


def push_run(workflow, sha):
    runs = json.loads(
        gh(
            "run",
            "list",
            "--workflow",
            workflow,
            "--commit",
            sha,
            "--event",
            "push",
            "--limit",
            "1",
            "--json",
            "databaseId,conclusion",
        )
    )
    return runs[0] if runs else None


def main(latest, commit):
    shas = (
        run(
            "git", "rev-list", "--first-parent", "--reverse", latest + ".." + commit
        ).split()
        if latest
        else [commit]
    )
    cache = {}

    def jobs(run_id):
        if run_id not in cache:
            data = json.loads(gh("run", "view", str(run_id), "--json", "jobs"))
            cache[run_id] = {job["name"]: job["conclusion"] for job in data["jobs"]}
        return cache[run_id]

    failed = [
        "ci.yml on " + sha + ": " + reason
        for sha, reason in blocking(
            [(sha, push_run("ci.yml", sha)) for sha in shas], jobs
        )
    ]
    for line in failed:
        print("::error::" + line)
    if failed:
        print(
            "::error::the push runs above have not succeeded; re-running a failed run clears it"
        )
        sys.exit(1)


if __name__ == "__main__":
    main(*sys.argv[1:])
