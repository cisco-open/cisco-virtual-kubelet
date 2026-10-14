#!/usr/bin/env python3
"""Run pinned upstream fixture comparisons. Credentials stay in the environment.

Creates and removes only the selected checked-in fixtures. Do not run alongside
another writer using the same fixture names or this Terraform state directory.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("--terraform", default="terraform")
parser.add_argument("--execute", action="store_true")
parser.add_argument("--fixture-set", choices=["core", "system-features"], default="core")
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
oracle = root / "examples/configs/sdwan/oracle"
sets = {
    "core": {"files": ["banner", "system-profile", "policy-objects", "network-hierarchy", "feature-profiles"], "count": 44,
             "absent": "Fixture|Policy|Hierarchy|Family", "oracle": "Banner|SystemProfile|Policy|Hierarchy|Family"},
    "system-features": {"files": ["system-features"], "count": 12, "absent": "Extra", "oracle": "Extra"},
}
selected = sets[args.fixture_set]
variables = ["-var=fixture_files=" + json.dumps(["../" + x + ".yaml" for x in selected["files"]])]
env = {k: v for k, v in os.environ.items() if not k.startswith("TF_LOG")}
for key in ("SDWAN_URL", "SDWAN_USERNAME", "SDWAN_PASSWORD"):
    if not env.get(key):
        raise SystemExit(f"{key} is required")
env.update(CVK_SDWAN_LIVE="1", CVK_SDWAN_ORACLE="1")
allowed = {
    'sdwan_app_probe_class_policy_object',
    'sdwan_application_list_policy_object',
    'sdwan_application_priority_feature_profile',
    'sdwan_application_priority_policy_settings_policy',
    'sdwan_as_path_list_policy_object',
    'sdwan_cisco_banner_feature_template',
    'sdwan_class_map_policy_object',
    'sdwan_cli_config_feature',
    'sdwan_cli_feature_profile',
    'sdwan_color_list_policy_object',
    'sdwan_configuration_group',
    'sdwan_data_fqdn_prefix_list_policy_object',
    'sdwan_data_ipv4_prefix_list_policy_object',
    'sdwan_data_ipv6_prefix_list_policy_object',
    'sdwan_embedded_security_feature_profile',
    'sdwan_expanded_community_list_policy_object',
    'sdwan_extended_community_list_policy_object',
    'sdwan_ipv4_prefix_list_policy_object',
    'sdwan_ipv6_prefix_list_policy_object',
    'sdwan_local_application_list_policy_object',
    'sdwan_mirror_policy_object',
    'sdwan_network_hierarchy_node',
    'sdwan_other_feature_profile',
    'sdwan_policer_policy_object',
    'sdwan_port_list_policy_object',
    'sdwan_preferred_color_group_policy_object',
    'sdwan_region_list_policy_object',
    'sdwan_service_feature_profile',
    'sdwan_site_list_policy_object',
    'sdwan_sla_class_policy_object',
    'sdwan_sse_feature_profile',
    'sdwan_standard_community_list_policy_object',
    'sdwan_system_banner_feature',
    'sdwan_system_basic_feature',
    'sdwan_system_bfd_feature',
    'sdwan_system_feature_profile',
    'sdwan_system_flexible_port_speed_feature',
    'sdwan_system_global_feature',
    'sdwan_system_logging_feature',
    'sdwan_system_mrf_feature',
    'sdwan_system_ntp_feature',
    'sdwan_system_omp_feature',
    'sdwan_system_performance_monitoring_feature',
    'sdwan_tloc_list_policy_object',
    'sdwan_topology_feature_profile',
    'sdwan_transport_feature_profile',
    'sdwan_vpn_list_policy_object',
    'sdwan_zone_list_policy_object',
    'terraform_data',
}

def run(command, *, show=False):
    result = subprocess.run(command, cwd=root, env=env, capture_output=True, text=True)
    if show:
        print(result.stdout, flush=True)
    if result.returncode:
        raise RuntimeError(f"{command[0]} {command[1]} failed (exit {result.returncode}); raw output withheld")
    return result.stdout

tf = [args.terraform, f"-chdir={oracle}"]
def plan(filename, targets=()):
    run(tf + ["plan", "-no-color", "-out=" + filename] + variables + ["-target=" + t for t in targets])
    return json.loads(run(tf + ["show", "-json", filename])).get("resource_changes", [])

def validate(changes, types=allowed):
    for change in changes:
        if change["type"] not in types or change["change"]["actions"] not in (["no-op"], ["create"]):
            raise RuntimeError("Plan differs from the isolated fixture allow-list")

run(tf + ["init", "-lockfile=readonly", "-no-color"])
state = json.loads(run(tf + ["show", "-json"]))
if state.get("values", {}).get("root_module", {}).get("child_modules"):
    raise SystemExit("Oracle state already owns resources; inspect and settle it first")
run(["go", "test", "./internal/controlleradapter/sdwan", "-run", "^TestLive(" + selected["absent"] + ")NamesAbsent$", "-count=1"], show=True)
changes = plan("fixture.tfplan")
validate(changes)
if sum(x["change"]["actions"] == ["create"] for x in changes) != selected["count"] + 1:
    raise SystemExit("Plan differs from the expected fixture count")
print(f"Validated plan: {selected['count']} isolated SD-WAN resources; no device assignments", flush=True)
if args.execute:
    try:
        # The upstream region-list resource does not express this dependency.
        targets = sorted({x["address"].split("[")[0] for x in changes if x["type"] == "sdwan_network_hierarchy_node"})
        if targets:
            validate(plan("hierarchy.tfplan", targets), {"sdwan_network_hierarchy_node", "terraform_data"})
            run(tf + ["apply", "-no-color", "hierarchy.tfplan"])
            validate(plan("fixture.tfplan"))
        run(tf + ["apply", "-no-color", "fixture.tfplan"])
        run(["go", "test", "./internal/controlleradapter/sdwan", "-run", "^TestLive(" + selected["oracle"] + ")Oracle$", "-v", "-count=1"], show=True)
    finally:
        if any(x["type"] == "sdwan_region_list_policy_object" for x in changes):
            run(tf + ["destroy", "-auto-approve", "-no-color", "-target=module.sdwan.sdwan_region_list_policy_object.region_list_policy_object"] + variables)
        run(tf + ["destroy", "-auto-approve", "-no-color"] + variables)
        print("Upstream fixtures destroyed", flush=True)
