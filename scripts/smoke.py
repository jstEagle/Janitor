#!/usr/bin/env python3
"""Exercise a built binary against disposable fixtures only; optionally test launchd."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser()
parser.add_argument("binary", type=Path)
parser.add_argument("--launchd", action="store_true")
args = parser.parse_args()
binary = str(args.binary.resolve())

with tempfile.TemporaryDirectory(prefix="janitor-smoke-") as temporary:
    base = Path(temporary).resolve()
    repo = base / "projects" / "app"
    repo.mkdir(parents=True)

    def git(*arguments):
        subprocess.run(
            ["git", "-C", str(repo), "-c", "user.name=Fixture", "-c",
             "user.email=fixture@example.invalid", *arguments],
            check=True, capture_output=True, text=True,
        )

    git("init", "-b", "main")
    (repo / "package.json").write_text("{}\n")
    (repo / ".gitignore").write_text("node_modules/\n")
    git("add", ".")
    git("commit", "-m", "fixture")
    artifact = repo / "node_modules"
    artifact.mkdir()
    (artifact / "payload").write_bytes(b"x" * 1048576)
    old = time.time() - 3 * 86400
    for parent, dirs, files in os.walk(repo):
        dirs[:] = [d for d in dirs if d != ".git"]
        for name in files:
            os.utime(Path(parent) / name, (old, old))
        os.utime(parent, (old, old))
    prefix = [binary, "--config", str(base / "config.json"), "--state", str(base / "state"), "--json"]

    def cli(*arguments):
        response = subprocess.run([*prefix, *arguments], check=True, capture_output=True, text=True)
        return json.loads(response.stdout)

    cli("init", "--root", str(repo.parent))
    cli("enroll", str(repo), "--coordinated", "--artifacts", "node_modules")
    inventory = cli("scan")
    assert len(inventory["workspaces"]) == 1
    # Age only this temporary fixture's observation to avoid a two-day test wait.
    # Never use state editing as an operational cleanup workaround.
    state_path = base / "state" / "state.json"
    state = json.loads(state_path.read_text())
    state["observations"][str(repo)]["last_changed"] = "2020-01-01T00:00:00Z"
    state_path.write_text(json.dumps(state))
    plan = cli("plan")
    assert len(plan["actions"]) == 1, plan
    result = cli("apply", plan["id"], "--yes")
    assert result["status"] == "completed"
    assert not artifact.exists()
    assert (repo / "package.json").exists()
    print("PASS: binary init → enroll → scan → plan → apply; source preserved")

    if args.launchd:
        definition = subprocess.run([*prefix, "service", "render"], check=True, capture_output=True, text=True).stdout
        label = "sh.janitor.smoke." + str(os.getpid())
        definition = definition.replace("sh.janitor.agent", label)
        plist = base / "smoke.plist"
        plist.write_text(definition)
        subprocess.run(["plutil", "-lint", str(plist)], check=True)
        domain = "gui/" + str(os.getuid())
        started = False
        try:
            subprocess.run(["launchctl", "bootstrap", domain, str(plist)], check=True, capture_output=True)
            started = True
            time.sleep(2)
            service = subprocess.run(["launchctl", "print", domain + "/" + label], check=True, capture_output=True, text=True).stdout
            assert "state = running" in service, service
            assert (base / "state" / "inventory.json").exists()
            print("PASS: isolated launchd service started and stayed running")
        finally:
            if started:
                subprocess.run(["launchctl", "bootout", domain + "/" + label], check=True, capture_output=True)
