"""Validate immutable inputs before resuming preview publication."""

import json
import re
import sys


def validate(run, jobs, tag, commit, gateway_ref, fixture_ref):
    if not re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-preview\.[1-9][0-9]*", tag):
        raise ValueError("only preview releases can use publication recovery")
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("invalid source commit")
    if (run.get("event") != "push" or run.get("head_branch") != tag
            or run.get("head_sha") != commit or run.get("status") != "completed"
            or run.get("conclusion") != "failure"
            or run.get("path") != ".github/workflows/release.yml"):
        raise ValueError("source run must be the failed release of the exact immutable tag")
    required = {
        "preflight", "images", "source-sbom", "public-images", "image-scan",
        "Image smoke (amd64)", "Image smoke (arm64)",
        "archives (ubuntu-24.04, linux, amd64)",
        "archives (ubuntu-24.04-arm, linux, arm64)",
        "archives (macos-15-intel, darwin, amd64)",
        "archives (macos-15, darwin, arm64)",
    }
    by_name = {job["name"]: job for job in jobs["jobs"]}
    if len(by_name) != len(jobs["jobs"]):
        raise ValueError("ambiguous source jobs")
    if any(by_name.get(name, {}).get("conclusion") != "success" for name in required):
        raise ValueError("all source, archive and image gates must already have passed")
    failed = {name for name, job in by_name.items() if job.get("conclusion") == "failure"}
    if failed != {"first-run"}:
        raise ValueError("only first-run packaging failures can be resumed")
    failed_steps = {step["name"] for step in by_name["first-run"].get("steps", [])
                    if step.get("conclusion") == "failure"}
    if failed_steps != {"Package and verify digest-pinned first-run asset"}:
        raise ValueError("only the first-run packaging step can have failed")
    for name in ("publish-release", "post-publish", "promote-latest"):
        if by_name.get(name, {}).get("conclusion") != "skipped":
            raise ValueError("source run must not have published or promoted artifacts")
    for repository, ref in (
        ("agent-gateway", gateway_ref), ("agent-gateway-a2a-fixture", fixture_ref)
    ):
        if not re.fullmatch(r"ghcr\.io/agent-guide/" + repository + r"@sha256:[0-9a-f]{64}", ref):
            raise ValueError("only official immutable image digests are accepted")
    return {"tag": tag, "version": tag[1:], "source_commit": commit}


if __name__ == "__main__":
    run_file, jobs_file, tag, commit, gateway_ref, fixture_ref = sys.argv[1:]
    with open(run_file) as source:
        run = json.load(source)
    with open(jobs_file) as source:
        jobs = json.load(source)
    for key, value in validate(run, jobs, tag, commit, gateway_ref, fixture_ref).items():
        print(f"{key}={value}")
