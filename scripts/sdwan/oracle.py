#!/usr/bin/env python3
"""Run the pinned upstream fixture oracle. Uses env credentials, never logs bodies.

Run in the SD-WAN worktree. This creates and removes ONLY the checked fixture
resources. Do not run alongside CVK managing the same fixtures.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("--terraform", default="terraform")
parser.add_argument("--execute", action="store_true", help="create fixtures, compare, and destroy")
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
oracle = root / "examples/configs/sdwan/oracle"
env = os.environ.copy()
for key in ("SDWAN_URL", "SDWAN_USERNAME", "SDWAN_PASSWORD"):
    if not env.get(key):
        raise SystemExit(f"{key} is required")
env.pop("TF_LOG", None)
env.pop("TF_LOG_PATH", None)
env.update(CVK_SDWAN_LIVE="1", CVK_SDWAN_ORACLE="1")

def run(command, *, show=False):
    result = subprocess.run(command, cwd=root, env=env, capture_output=True, text=True)
    if show:
        print(result.stdout)
    if result.returncode:
        raise RuntimeError(f"{command[0]} {command[1]} failed (exit {result.returncode}); raw output withheld")
    return result.stdout

tf = [args.terraform, f"-chdir={oracle}"]
run(tf + ["init", "-lockfile=readonly", "-no-color"])
state = json.loads(run(tf + ["show", "-json"]))
if state.get("values", {}).get("root_module", {}).get("child_modules"):
    raise SystemExit("Oracle state already owns resources; inspect and settle it first")
run(["go", "test", "./internal/controlleradapter/sdwan", "-run", "^TestLiveFixtureNamesAbsent$", "-count=1"], show=True)
run(tf + ["plan", "-out=fixture.tfplan", "-no-color"])
plan = json.loads(run(tf + ["show", "-json", "fixture.tfplan"]))
allowed = {"sdwan_cisco_banner_feature_template", "sdwan_system_feature_profile", "sdwan_system_banner_feature", "sdwan_configuration_group", "terraform_data"}
changes = [x for x in plan.get("resource_changes", []) if x["change"]["actions"] != ["no-op"]]
if len(changes) != 5 or any(x["type"] not in allowed or x["change"]["actions"] != ["create"] for x in changes):
    raise SystemExit("Plan does not match the isolated fixture allow-list")
print("Validated plan: four isolated SD-WAN resources; no device assignments")
if args.execute:
    try:
        run(tf + ["apply", "-no-color", "fixture.tfplan"])
        run(["go", "test", "./internal/controlleradapter/sdwan", "-run", "^TestLive(Banner|SystemProfile)Oracle$", "-v", "-count=1"], show=True)
    finally:
        run(tf + ["destroy", "-auto-approve", "-no-color"])
        print("Upstream fixtures destroyed")
