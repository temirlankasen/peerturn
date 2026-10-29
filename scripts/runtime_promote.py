"""Promote real matrix evidence atomically, with protected-main PR fallback."""
import base64
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import re
import runpy
import sys
import tempfile
import subprocess


C = runpy.run_path(str(Path(__file__).with_name("runtime_common.py")))
E = C["E"]
AGE = runpy.run_path(str(Path(__file__).with_name("runtime_age.py")))
BOT_AUTHOR = {"name": "PeerTurn Automation", "email": "41898282+github-actions@users.noreply.github.com"}


class StaleQualification(ValueError):
    """A new green run is needed; old source evidence cannot authorize release."""


class UntrustedAutomation(ValueError):
    """An occupied branch/PR cannot be reused for this exact verified change."""


def supersede_pending(ledger, runtime, version, tag, published):
    for entry in ledger:
        if (entry["runtime"], entry["version"]) == (runtime, version) and entry["tag"] not in published:
            entry.setdefault("superseded_by", tag)


def requested_runs(api):
    run_id = os.environ.get("QUALIFICATION_RUN", "")
    if run_id:
        if not re.fullmatch(r"[1-9][0-9]{0,19}", run_id):
            raise ValueError("invalid qualification run ID")
        expected = (os.environ["QUAL_RUNTIME"], os.environ["QUAL_VERSION"])
        C["version_tuple"](*expected)
        return [(api.get("actions/runs/" + run_id), expected)]
    # A fixed seven-day window covers the 72-hour hold without scanning history.
    # Older runs remain recoverable by an explicit, independently checked run ID.
    since = (datetime.now(timezone.utc) - timedelta(days=7)).strftime("%Y-%m-%dT%H:%M:%SZ")
    candidates = {}
    for page in range(1, 4):
        runs = api.get("actions/workflows/runtime-qualify.yml/runs?branch=main&event=workflow_dispatch"
                       f"&created=%3E%3D{since}&per_page=100&page={page}")["workflow_runs"]
        candidates.update({run["id"]: (run, None) for run in runs})
        if len(runs) < 100:
            break
    return sorted(candidates.values(), key=lambda item: item[0]["id"], reverse=True)


def qualification(api, run, expected=None):
    match = re.fullmatch(r"Qualify (claude|codex) ([0-9.]+)", run["display_title"])
    if not match or run["head_branch"] != "main" or run["event"] != "workflow_dispatch":
        raise ValueError("untrusted qualification run")
    if run["path"] != ".github/workflows/runtime-qualify.yml" or run["head_repository"]["full_name"] != api.repo:
        raise ValueError("foreign qualification workflow")
    if run.get("conclusion") in {"cancelled", "timed_out", "action_required", "skipped"}:
        raise ValueError("qualification was cancelled or interrupted")
    runtime, version = match.groups()
    if expected is not None and expected != (runtime, version):
        raise ValueError("qualification input mismatch")
    C["version_tuple"](runtime, version)
    jobs = api.pages(f"actions/runs/{run['id']}/jobs?filter=latest", "jobs")
    required = {"Model-free contracts", "Live qualification / ubuntu-latest", "Live qualification / macos-latest"}
    matching = [(name, job["conclusion"]) for job in jobs for name in required
                if job["name"] == name]
    observed = dict(matching)
    if len(matching) != len(required) or set(observed) != required or any(value != "success" for value in observed.values()):
        raise ValueError("qualification matrix incomplete or failed")
    return runtime, version


def tree(api, sha):
    commit = api.get("git/commits/" + sha)
    data = api.get("git/trees/" + commit["tree"]["sha"] + "?recursive=1")
    if data["truncated"]:
        raise ValueError("repository tree truncated")
    return commit["tree"]["sha"], {entry["path"]: entry for entry in data["tree"] if entry["type"] == "blob"}


def same_code(a, b, api=None):
    ignored = C["EDITABLE"] - C["PIN_FILES"]
    if set(a) - ignored != set(b) - ignored:
        return False
    for path in set(a) - ignored:
        if a[path].get("mode", "100644") != b[path].get("mode", "100644"):
            return False
        if a[path]["sha"] == b[path]["sha"]:
            continue
        if path not in C["PIN_FILES"] or api is None:
            return False
        normalize = lambda text: re.sub(r"(?<![0-9])0\.1\.(?:0|[1-9][0-9]*)-preview", "RELEASE_PIN", text)
        if normalize(blob(api, a[path])) != normalize(blob(api, b[path])):
            return False
    return True


def blob(api, entry):
    data = api.get("git/blobs/" + entry["sha"])
    if data["encoding"] != "base64" or data["size"] > 100000:
        raise ValueError("invalid bookkeeping blob")
    return base64.b64decode(data["content"], validate=False).decode()


def enable_merge(api, branch, sha):
    pulls = api.pages(f"pulls?state=open&head={api.repo.split('/')[0]}:{branch}&base=main")
    if len(pulls) > 1:
        raise ValueError("ambiguous automation PR")
    if pulls:
        pr = pulls[0]
    else:
        pr = api.put("pulls", {"title": "chore: record verified runtime qualification", "head": branch,
            "base": "main", "body": "Automated, evidence-backed runtime verification. Required checks must pass before auto-merge."})
    if (pr.get("user", {}).get("login") != "github-actions[bot]" or pr.get("user", {}).get("type") != "Bot"
            or pr.get("head", {}).get("sha") != sha or pr["head"].get("ref") != branch
            or (pr["head"].get("repo") or {}).get("full_name") != api.repo
            or pr.get("base", {}).get("ref") != "main"
            or (pr["base"].get("repo") or {}).get("full_name") != api.repo):
        raise UntrustedAutomation("untrusted automation PR identity or head")
    # Pull request API objects do not expose App provenance; the issue view does.
    issue = api.get(f"issues/{pr['number']}")
    if (issue.get("user", {}).get("login") != "github-actions[bot]"
            or (issue.get("performed_via_github_app") or {}).get("slug") != "github-actions"):
        raise UntrustedAutomation("untrusted automation PR app")
    # Explicit dispatch works with GITHUB_TOKEN; the resulting checks belong to
    # the bot head SHA. Never assume the bot-created PR starts ordinary CI.
    checks = api.pages(f"actions/workflows/ci.yml/runs?branch={branch}&head_sha={sha}", "workflow_runs", max_pages=1)
    claim = f"<!-- ci-dispatch {sha} -->"
    if not any(run["head_sha"] == sha for run in checks) and claim not in (pr.get("body") or ""):
        api.put(f"pulls/{pr['number']}", {"body": (pr.get("body") or "") + "\n\n" + claim}, "PATCH")
        api.put("actions/workflows/ci.yml/dispatches", {"ref": branch})
    mutation = "mutation($id:ID!){enablePullRequestAutoMerge(input:{pullRequestId:$id,mergeMethod:SQUASH}){pullRequest{number}}}"
    if not pr.get("auto_merge"):
        result = api.request("graphql", {"query": mutation, "variables": {"id": pr["node_id"]}})
        if result.get("errors"):
            raise ValueError("enable auto-merge/required checks in repository settings")
    print(f"Protected main: qualification PR #{pr['number']} awaits required checks ({sha[:12]})")


def automation_branch(api, branch, commit, expected_tree, main):
    try:
        api.put("git/refs", {"ref": "refs/heads/" + branch, "sha": commit})
    except C["APIError"] as error:
        if error.status != 422:
            raise
        commit = api.get("git/ref/heads/" + branch)["object"]["sha"]
        existing = api.get("git/commits/" + commit)
        if (existing["tree"]["sha"] != expected_tree or [parent["sha"] for parent in existing.get("parents", [])] != [main]
                or any(any(existing.get(role, {}).get(key) != value for key, value in BOT_AUTHOR.items())
                       for role in ("author", "committer"))):
            raise UntrustedAutomation("existing automation branch has unexpected tree or author")
    return commit


def promote(api, run, expected=None):
    runtime, version = qualification(api, run, expected)
    if not AGE["mature"](api, runtime, version):
        print("Qualification retained; verified label waits for 72 hours since upstream publication")
        return
    pending = api.pages("pulls?state=open&base=main")
    branch = f"automation/verified-{runtime}-{version}-{run['id']}"
    own_ref = re.compile(re.escape(branch) + r"(?:-[0-9a-f]{12})?")
    own_replacements = [pr["head"]["ref"] for pr in pending
                        if own_ref.fullmatch(pr.get("head", {}).get("ref", "")) and pr["head"]["ref"] != branch
                        and (pr["head"].get("repo") or {}).get("full_name") == api.repo
                        and pr.get("user", {}).get("login") == "github-actions[bot]"]
    if len(own_replacements) > 1:
        raise UntrustedAutomation("ambiguous replacement PRs")
    if any(re.fullmatch(r"automation/verified-(?:claude|codex)-[0-9]+\.[0-9]+\.[0-9]+-[0-9]+(?:-[0-9a-f]{12})?", pr.get("head", {}).get("ref", ""))
           and (pr.get("head", {}).get("repo") or {}).get("full_name") == api.repo
           and pr.get("base", {}).get("repo", {}).get("full_name") == api.repo
           and not own_ref.fullmatch(pr["head"]["ref"])
           and pr.get("user", {}).get("login") == "github-actions[bot]" for pr in pending):
        print("A verification PR is pending; defer this promotion to the next sweep")
        return
    main = api.get("git/ref/heads/main")["object"]["sha"]
    base_tree, entries = tree(api, main)
    _, qualified = tree(api, run["head_sha"])
    if not same_code(entries, qualified, api):
        raise StaleQualification("code changed after qualification; rerun on current main")
    platforms = E["qualified_platforms"](api, run, runtime, version)
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        for path in C["EDITABLE"]:
            target = root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(blob(api, entries[path]) if path in entries else "[]\n")
        evidence = f"https://github.com/{api.repo}/actions/runs/{run['id']}"
        if not C["update_records"](root, runtime, version, evidence, platforms):
            return
        ledger = json.loads((root / C["RELEASES"]).read_text())
        tags = [item["name"] for item in api.pages("tags")]
        tag = C["next_tag"](tags + [item["tag"] for item in ledger])
        C["bump_pins"](root, tag)
        published = {item["tag_name"] for item in api.pages("releases") if not item["draft"]}
        supersede_pending(ledger, runtime, version, tag, published)
        ledger.append({"runtime": runtime, "version": version, "run_id": run["id"],
                       "qualified_sha": run["head_sha"], "tag": tag, "policy": C["POLICY"], "platforms": platforms})
        (root / C["RELEASES"]).write_text(json.dumps(ledger, indent=2) + "\n")
        changes = [{"path": path, "mode": entries.get(path, {}).get("mode", "100644"), "type": "blob", "content": (root / path).read_text()}
                   for path in sorted(C["EDITABLE"])]
    new_tree = api.put("git/trees", {"base_tree": base_tree, "tree": changes})["sha"]
    message = (f"chore: verify {runtime} {version}\n\n{evidence}\n\n"
               "Signed-off-by: PeerTurn Automation <41898282+github-actions@users.noreply.github.com>")
    commit = api.put("git/commits", {"message": message, "tree": new_tree, "parents": [main],
                                    "author": BOT_AUTHOR, "committer": BOT_AUTHOR})["sha"]
    try:
        api.put("git/refs/heads/main", {"sha": commit, "force": False}, "PATCH")
        print(f"Recorded verified runtime in main ({commit[:12]})")
    except C["APIError"] as error:
        if error.status not in {403, 422}:
            raise
        # A collision gets one fresh branch/PR; never delete, force or adopt an
        # occupied branch whose tree/identity differs from this exact change.
        try:
            head = automation_branch(api, branch, commit, new_tree, main)
            enable_merge(api, branch, head)
        except UntrustedAutomation:
            branch = own_replacements[0] if own_replacements else branch + "-" + commit[:12]
            head = automation_branch(api, branch, commit, new_tree, main)
            enable_merge(api, branch, head)


def reconcile_releases(api):
    main = api.get("git/ref/heads/main")["object"]["sha"]
    _, entries = tree(api, main)
    if C["RELEASES"] not in entries:
        return
    ledger = json.loads(blob(api, entries[C["RELEASES"]]))
    releases = {item["tag_name"]: item for item in api.pages("releases")}
    stale = False
    for item in ledger:
        if item.get("superseded_by"):
            continue
        tag = item["tag"]
        if not re.fullmatch(r"v0\.1\.(0|[1-9][0-9]{0,8})-preview", tag):
            raise ValueError("invalid reserved release tag")
        if tag in releases and not releases[tag]["draft"]:
            continue
        C["version_tuple"](item["runtime"], item["version"])
        if not AGE["mature"](api, item["runtime"], item["version"]):
            continue
        run = api.get(f"actions/runs/{item['run_id']}")
        if qualification(api, run, (item["runtime"], item["version"])) != (item["runtime"], item["version"]):
            raise ValueError("qualification record mismatch")
        if item.get("platforms") != E["qualified_platforms"](api, run, item["runtime"], item["version"]):
            raise ValueError("qualification platform record mismatch")
        _, qualified = tree(api, item["qualified_sha"])
        if run["head_sha"] != item["qualified_sha"] or not same_code(entries, qualified, api):
            stale = True
            continue
        try:
            ref = api.get("git/ref/tags/" + tag)
        except C["APIError"] as error:
            if error.status != 404:
                raise
            ref = api.put("git/refs", {"ref": "refs/tags/" + tag, "sha": main})
        if ref["object"]["type"] != "commit":
            raise ValueError("automation tag must be a lightweight commit tag")
        _, tagged = tree(api, ref["object"]["sha"])
        if not same_code(tagged, qualified, api) or C["RELEASES"] not in tagged:
            raise ValueError("existing tag has unrelated content")
        if item not in json.loads(blob(api, tagged[C["RELEASES"]])):
            raise ValueError("tag lacks exact qualification receipt")
        runs = api.pages("actions/workflows/release.yml/runs?event=workflow_dispatch"
                         f"&branch={tag}&head_sha={ref['object']['sha']}", "workflow_runs", max_pages=1)
        attempts = [entry for entry in runs if entry["head_sha"] == ref["object"]["sha"]
                    and entry["head_branch"] == tag]
        if attempts:
            # A failed release is rerun by an integrator; never overwrite a
            # published release or start a second signing job automatically.
            continue
        issue = C["issue_for"](api, item["runtime"], item["version"])
        claim = f"<!-- release-dispatch {tag} -->"
        if issue and claim in (issue.get("body") or ""):
            continue
        body = ((issue.get("body") or "") if issue else "") + "\n\n" + claim + "\n"
        if issue:
            api.put(f"issues/{issue['number']}", {"body": body}, "PATCH")
        else:
            issue = api.put("issues", {"title": C["title"](item["runtime"], item["version"]), "body": body})
        # Dispatch AT THE TAG: preserves the installer's exact cosign OIDC
        # identity release.yml@refs/tags/v..., unlike a reusable call from main.
        api.put("actions/workflows/release.yml/dispatches", {"ref": tag})
        print(f"Explicitly dispatched signed release {tag}")
        if issue:
            api.put(f"issues/{issue['number']}", {"state": "closed"}, "PATCH")
    if stale:
        raise StaleQualification("pending release needs fresh qualification on current main")


def main():
    C["validate_context"](os.environ)
    api = C["GitHub"]()
    if sys.argv[1:] == ["current"]:
        expected = (os.environ["QUAL_RUNTIME"], os.environ["QUAL_VERSION"])
        promote(api, api.get("actions/runs/" + os.environ["GITHUB_RUN_ID"]), expected)
    elif sys.argv[1:] == ["reconcile"]:
        records = json.loads((C["ROOT"] / C["RECORDS"]).read_text())
        seen = set()
        examined = 0
        for run, expected in requested_runs(api):
            if run["status"] not in {"completed", "in_progress"}:
                continue
            match = re.fullmatch(r"Qualify (claude|codex) ([0-9.]+)", run["display_title"])
            if not match:
                continue
            runtime, version = match.groups()
            if (runtime, version) in seen:
                continue
            seen.add((runtime, version))
            evidence = f"https://github.com/{api.repo}/actions/runs/{run['id']}"
            recorded = any(item["version"] == version and evidence in item["reference"].split("; ") for item in records[runtime])
            if not recorded:
                if examined == 16:
                    break
                examined += 1
                try:
                    qualification(api, run, expected)
                except ValueError:
                    continue
                try:
                    promote(api, run, expected)
                except StaleQualification:
                    continue
                # Use current API main for every update, not the checkout snapshot.
                _, entries = tree(api, api.get("git/ref/heads/main")["object"]["sha"])
                records = json.loads(blob(api, entries[C["RECORDS"]]))
    else:
        raise ValueError("expected current/reconcile")
    reconcile_releases(api)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, C["APIError"], subprocess.SubprocessError):
        print("Promotion stopped; inspect qualification freshness, required checks and repository settings", file=sys.stderr)
        sys.exit(1)
