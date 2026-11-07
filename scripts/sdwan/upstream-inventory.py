#!/usr/bin/env python3
"""Inventory the pinned module without confusing declared coverage with parity."""
import argparse
import json
from pathlib import Path
import re
import subprocess

p = argparse.ArgumentParser()
p.add_argument("module", type=Path)
p.add_argument("--output", type=Path, required=True)
a = p.parse_args()
commit = subprocess.check_output(["git", "-C", str(a.module), "rev-parse", "HEAD"], text=True).strip()
if commit != "5fbf037ea57368c7e6e77694c81330b6f7b16666":
    raise SystemExit("Reference checkout must be the pinned v1.5.0 commit")
resources = []
for source in sorted(a.module.glob("*.tf")):
    for kind, name in re.findall(r'resource\s+"(sdwan_[^"]+)"\s+"([^"]+)"', source.read_text()):
        resources.append(dict(type=kind, name=name, source=source.name))
a.output.write_text(json.dumps(dict(module_commit=commit, resources=resources), indent=2) + "\n")
print(f"Inventoried {len(resources)} upstream declarations")
