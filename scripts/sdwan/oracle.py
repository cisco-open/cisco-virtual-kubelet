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
parser.add_argument("--fixture-set", choices=["core", "system-features", "peripheral-features", "thousandeyes-feature", "policy-profile", "snmp-feature", "aaa-feature", "classic-templates", "classic-cellular", "classic-bfd", "classic-igmp", "classic-ntp", "classic-omp", "classic-pim", "classic-applications", "classic-dhcp", "service-features", "transport-cellular", "classic-ipsec", "classic-snmp", "classic-logging"], default="core")
args = parser.parse_args()
if args.fixture_set == "policy-profile" and args.execute:
    raise SystemExit("Policy-profile execution needs a qualified empty-controller or read-only-parent workflow; plan-only is available")
root = Path(__file__).resolve().parents[2]
oracle = root / "examples/configs/sdwan/oracle"
sets = {
 "classic-logging": {"files": ["classic-logging"], "count": 1, "absent": "ClassicLogging", "oracle": "ClassicLogging"},
 "classic-snmp": {"files": ["classic-snmp"], "count": 1, "absent": "ClassicSNMP", "oracle": "ClassicSNMP"},
 "classic-ipsec": {"files": ["classic-ipsec"], "count": 1, "absent": "IPSec", "oracle": "IPSec"},
 "transport-cellular": {"files": ["transport-cellular"], "count": 4, "absent": "TransportCellular", "oracle": "TransportCellular"},
 "service-features": {"files": ["service-features"], "count": 9, "absent": "Service", "oracle": "Service"},
 "classic-dhcp": {"files": ["classic-dhcp"], "count": 1, "absent": "DHCP", "oracle": "DHCP"},
 "classic-applications": {"files": ["classic-applications"], "count": 2, "absent": "Applications", "oracle": "Applications"},
 "classic-pim": {"files": ["classic-pim"], "count": 1, "absent": "PIM", "oracle": "PIM"},
 "classic-omp": {"files": ["classic-omp"], "count": 1, "absent": "OMP", "oracle": "OMP"},
 "classic-ntp": {"files": ["classic-ntp"], "count": 1, "absent": "NTP", "oracle": "NTP"},
 "classic-igmp": {"files": ["classic-igmp"], "count": 1, "absent": "IGMP", "oracle": "IGMP"},
 "classic-bfd": {"files": ["classic-bfd"], "count": 1, "absent": "BFD", "oracle": "BFD"},
 "classic-cellular": {"files": ["classic-cellular"], "count": 2, "absent": "Cellular", "oracle": "Cellular"},
 "classic-templates": {"files": ["classic-templates"], "count": 3, "absent": "Classic", "oracle": "Classic"},
 "aaa-feature": {"files": ["aaa-feature"], "count": 2, "absent": "AAA", "oracle": "AAA"},
 "snmp-feature": {"files": ["snmp-feature"], "count": 2, "absent": "SNMP", "oracle": "SNMP"},
 "policy-profile": {"files": ["policy-profile"], "count": 16, "absent": "PolicyProfile", "oracle": "PolicyProfile"},
 "thousandeyes-feature": {"files": ["thousandeyes-feature"], "count": 2, "absent": "ThousandEyes", "oracle": "ThousandEyes"},
 "peripheral-features": {"files": ["peripheral-features"], "count": 4, "absent": "Peripheral", "oracle": "Peripheral"},
    "core": {"files": ["banner", "system-profile", "policy-objects", "network-hierarchy", "feature-profiles"], "count": 44,
             "absent": "Fixture|Policy|Hierarchy|Family", "oracle": "Banner|SystemProfile|Policy|Hierarchy|Family"},
    "system-features": {"files": ["system-features"], "count": 13, "absent": "Extra", "oracle": "Extra"},
}
selected = sets[args.fixture_set]
variables = ["-var=fixture_files=" + json.dumps(["../" + x + ".yaml" for x in selected["files"]])]
env = {k: v for k, v in os.environ.items() if not k.startswith("TF_LOG")}
for key in ("SDWAN_URL", "SDWAN_USERNAME", "SDWAN_PASSWORD"):
    if not env.get(key):
        raise SystemExit(f"{key} is required")
env.update(CVK_SDWAN_LIVE="1", CVK_SDWAN_ORACLE="1")
allowed = {
    "sdwan_cisco_logging_feature_template",
    "sdwan_cisco_snmp_feature_template",
    "sdwan_cisco_vpn_interface_ipsec_feature_template",
    "sdwan_service_tracker_group_feature",
    "sdwan_service_object_tracker_group_feature",
    "sdwan_transport_cellular_controller_feature",
    "sdwan_transport_cellular_profile_feature",
    "sdwan_service_object_tracker_feature",
    "sdwan_service_tracker_feature",
    "sdwan_service_dhcp_server_feature",
    "sdwan_cisco_dhcp_server_feature_template",
    "sdwan_cisco_thousandeyes_feature_template",
    "sdwan_security_app_hosting_feature_template",
    "sdwan_cedge_pim_feature_template",
    "sdwan_cisco_omp_feature_template",
    'sdwan_policy_object_feature_profile',
    'sdwan_policy_object_as_path_list',
    'sdwan_policy_object_class_map',
    'sdwan_policy_object_color_list',
    'sdwan_policy_object_extended_community_list',
    'sdwan_policy_object_mirror',
    'sdwan_policy_object_policer',
    'sdwan_policy_object_security_data_ipv4_prefix_list',
    'sdwan_policy_object_security_fqdn_list',
    'sdwan_policy_object_security_local_domain_list',
    'sdwan_policy_object_security_port_list',
    'sdwan_policy_object_security_protocol_list',
    'sdwan_policy_object_security_url_allow_list',
    'sdwan_policy_object_security_url_block_list',
    'sdwan_policy_object_standard_community_list',
    'sdwan_policy_object_tloc_list',

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
    'sdwan_other_ucse_feature',
    'sdwan_other_thousandeyes_feature',
    'sdwan_transport_gps_feature',
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
    'sdwan_system_security_feature',
    'sdwan_system_snmp_feature',
    'sdwan_system_aaa_feature',
    'sdwan_cedge_global_feature_template',
    'sdwan_cedge_multicast_feature_template',
    'sdwan_cli_template_feature_template',
    'sdwan_cellular_controller_feature_template',
    'sdwan_cisco_bfd_feature_template',
    'sdwan_cedge_igmp_feature_template',
    'sdwan_cisco_ntp_feature_template',
    'sdwan_cellular_cedge_profile_feature_template',
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
        if change.get("mode") == "data" and change["type"] == "sdwan_policy_object_feature_profile_parcels" and change["change"]["actions"] in (["read"], ["no-op"]):
            continue
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
