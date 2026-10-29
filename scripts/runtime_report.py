"""One sanitized failure issue per runtime version, without native transcripts."""
import os
from pathlib import Path
import runpy
import re
import sys


C = runpy.run_path(str(Path(__file__).with_name("runtime_common.py")))
ALLOWED_JOBS = {"Model-free contracts", "Live qualification / ubuntu-latest", "Live qualification / macos-latest",
                "Record verification and release"}
ALLOWED_STEPS = {"Validate main-only inputs", "Model-free adapter contracts", "Install exact public runtimes",
                 "Build PeerTurn", "Live native contract qualification", "Validate candidate schemas",
                 "Record green qualification and request signed release", "Pin candidate and baseline artifact identities"}
CONCLUSIONS = {"success", "failure", "cancelled", "skipped", "timed_out", "action_required", None}


def main():
    C["validate_context"](os.environ)
    runtime, version = sys.argv[1:]
    C["version_tuple"](runtime, version)
    api = C["GitHub"]()
    run_id = os.environ["GITHUB_RUN_ID"]
    if not run_id.isdecimal():
        raise ValueError("invalid run ID")
    jobs = api.pages(f"actions/runs/{run_id}/jobs?filter=latest", "jobs")
    summary = []
    for job in jobs:
        name = next((name for name in ALLOWED_JOBS if job["name"] == name or job["name"].endswith(" / " + name)), None)
        if name is None:
            continue
        conclusion = job.get("conclusion")
        if conclusion not in CONCLUSIONS:
            raise ValueError("unknown job conclusion")
        summary.append(f"- {name}: {conclusion or 'incomplete'}")
        for step in job.get("steps", []):
            if step["name"] in ALLOWED_STEPS and step.get("conclusion") in CONCLUSIONS - {"success", "skipped", None}:
                summary.append(f"  - {step['name']}: {step['conclusion']}")
    issue = C["issue_for"](api, runtime, version)
    original = (issue.get("body") or "") if issue else ""
    # Keep the durable dispatch claim, but replace all previous failure summaries.
    claim = "\n".join(re.findall(r"<!-- (?:runtime-watch|release-dispatch) [^\r\n<>]{1,512} -->", original))
    body = (claim + f"\n\nQualification failed or was incomplete for {runtime} {version}.\n\n"
            + "\n".join(summary) + f"\n\n[Run](https://github.com/{api.repo}/actions/runs/{run_id}).\n"
            "No verification/release was authorized. Patch the failed contract or fix infrastructure, "
            "then rerun runtime-qualify on main. No raw native transcripts or provider errors are published.\n")
    if issue:
        api.put(f"issues/{issue['number']}", {"body": body, "state": "open"}, "PATCH")
    else:
        api.put("issues", {"title": C["title"](runtime, version), "body": body})


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, C["APIError"]):
        print("Qualification failure report could not be recorded", file=sys.stderr)
        sys.exit(1)
