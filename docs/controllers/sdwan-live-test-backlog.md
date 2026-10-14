# SD-WAN implementation and live-test backlog

This is a progress inventory, not a claim of completed deployment parity. The pinned
NetAsCode v1.5.0 module contains 226 resource declarations. Current status:

| Status | Declarations |
|---|---:|
| Fixture-qualified against live Manager | 105 |
| Implemented with local fixtures; live tests outstanding | 35 |
| Implementation still pending | 85 |
| Compatibility-blocked (TrustSec) | 1 |

The latest increment qualifies two system device-access declarations, IPv4 and
IPv6. Both passed native lifecycle and live Kubernetes reconciliation with inline
prefixes. Prefix-list references passed compiler and dependency-order tests but
need a lab where CVK can create the singleton policy-object profile; this lab
already has one.

The previous increment qualified seven declarations: classic SVI, cellular and
switchport templates, service EIGRP, service switchport, service multicast, and Zscaler SSE. Each passed native live
create/read/update/read with cleanup and Kubernetes reconciliation. Qualification
covers unattached fixture attributes, not router behavior or full parity. The
previous increment qualified six routing/security declarations and six earlier
tracker/SSE/SIG definitions.

## Live tests required for implemented definitions

For each row, run native create/read/update/read/idempotence and receipt-bound cleanup,
followed by Kubernetes reconciliation with the selected fixture. Include dependency
creation order, missing-reference behavior and resolved IDs for reference-bearing
parcels. Do not attach these fixtures to routers as part of object qualification.

All 32 policy-object declarations and their profile need live qualification. The
current lab already has its singleton profile; an empty test Manager or an explicitly
implemented existing-parent workflow is needed. CVK does not adopt that profile.

| Upstream declaration | Native model path (under `sdwan`) | Fixture(s) | Prerequisite |
|---|---|---|---|
| `sdwan_custom_application` | `application_catalog.custom_applications` | [custom-applications.yaml](../../examples/configs/sdwan/custom-applications.yaml) | Enable SD-AVC on a test vManage; current Manager returns `POLICYVALIDATION0001` before creating the object |
| `sdwan_policy_object_feature_profile` | `feature_profiles.policy_object_profile` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Manager permits only one policy-object profile |
| `sdwan_system_ca_certificate_feature` | `feature_profiles.system_profiles.ca_certificate` | [ca-certificate.yaml](../../examples/configs/sdwan/ca-certificate.yaml) | Replace placeholder certificate ID with an existing lab certificate |
| `sdwan_policy_object_app_probe_class` | `feature_profiles.policy_object_profile.app_probe_classes` | [policy-dependencies.yaml](../../examples/configs/sdwan/policy-dependencies.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_application_list` | `feature_profiles.policy_object_profile.application_lists` | [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_as_path_list` | `feature_profiles.policy_object_profile.as_path_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_class_map` | `feature_profiles.policy_object_profile.forwarding_classes` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml), [policy-dependencies.yaml](../../examples/configs/sdwan/policy-dependencies.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_color_list` | `feature_profiles.policy_object_profile.color_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_data_ipv4_prefix_list` | `feature_profiles.policy_object_profile.ipv4_data_prefix_lists` | [policy-prefixes.yaml](../../examples/configs/sdwan/policy-prefixes.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_data_ipv6_prefix_list` | `feature_profiles.policy_object_profile.ipv6_data_prefix_lists` | [policy-prefixes.yaml](../../examples/configs/sdwan/policy-prefixes.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_expanded_community_list` | `feature_profiles.policy_object_profile.expanded_community_lists` | [policy-prefixes.yaml](../../examples/configs/sdwan/policy-prefixes.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_extended_community_list` | `feature_profiles.policy_object_profile.extended_community_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_ipv4_prefix_list` | `feature_profiles.policy_object_profile.ipv4_prefix_lists` | [policy-prefixes.yaml](../../examples/configs/sdwan/policy-prefixes.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_ipv6_prefix_list` | `feature_profiles.policy_object_profile.ipv6_prefix_lists` | [policy-prefixes.yaml](../../examples/configs/sdwan/policy-prefixes.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_mirror` | `feature_profiles.policy_object_profile.mirrors` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_policer` | `feature_profiles.policy_object_profile.policers` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_preferred_color_group` | `feature_profiles.policy_object_profile.preferred_color_groups` | [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_data_ipv4_prefix_list` | `feature_profiles.policy_object_profile.security_data_ipv4_prefix_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_fqdn_list` | `feature_profiles.policy_object_profile.security_fqdn_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_geolocation_list` | `feature_profiles.policy_object_profile.security_geo_location_lists` | [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_ips_signature` | `feature_profiles.policy_object_profile.security_ips_signature_lists` | [policy-prefixes.yaml](../../examples/configs/sdwan/policy-prefixes.yaml), [policy-dependencies.yaml](../../examples/configs/sdwan/policy-dependencies.yaml), [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_local_application_list` | `feature_profiles.policy_object_profile.security_local_application_lists` | [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_local_domain_list` | `feature_profiles.policy_object_profile.security_local_domain_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_port_list` | `feature_profiles.policy_object_profile.security_port_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_protocol_list` | `feature_profiles.policy_object_profile.security_protocol_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_url_allow_list` | `feature_profiles.policy_object_profile.security_url_allow_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml), [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_url_block_list` | `feature_profiles.policy_object_profile.security_url_block_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_security_zone` | `feature_profiles.policy_object_profile.security_zones` | [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_unified_url_filtering` | `feature_profiles.policy_object_profile.security_url_filtering_profiles` | [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_sla_class_list` | `feature_profiles.policy_object_profile.sla_classes` | [policy-dependencies.yaml](../../examples/configs/sdwan/policy-dependencies.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_standard_community_list` | `feature_profiles.policy_object_profile.standard_community_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_tloc_list` | `feature_profiles.policy_object_profile.tloc_lists` | [policy-profile.yaml](../../examples/configs/sdwan/policy-profile.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_unified_advanced_inspection_profile` | `feature_profiles.policy_object_profile.security_advanced_inspection_profiles` | [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_unified_advanced_malware_protection` | `feature_profiles.policy_object_profile.security_advanced_malware_protection_profiles` | [policy-prefixes.yaml](../../examples/configs/sdwan/policy-prefixes.yaml), [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |
| `sdwan_policy_object_unified_intrusion_prevention` | `feature_profiles.policy_object_profile.security_intrusion_prevention_profiles` | [policy-dependencies.yaml](../../examples/configs/sdwan/policy-dependencies.yaml), [policy-security.yaml](../../examples/configs/sdwan/policy-security.yaml) | Isolated policy-object profile; referenced parcels declared in the fixture |

The security-zone VPN entries follow the upstream string-valued VPN names. The
fixture uses an interface zone; testing VPN-name references needs declared service
LAN VPNs. See the [NaC security-zone model](https://netascode.cisco.com/docs/data_models/sdwan/policy_objects/security_zone/).

## Native test commands

Provide Manager credentials through the existing environment/secret mechanism.
The transport example below has passed; use the table above to select remaining
fixtures. Select one fixture at a time, starting with absent fixture names.

```bash
CVK_SDWAN_LIVE=1 CVK_SDWAN_MUTATE=1 \
CVK_SDWAN_PENDING_FIXTURE=transport-trackers \
go test ./internal/controlleradapter/sdwan -run "^TestLivePendingLifecycle$" -count=1 -v

# KUBEBUILDER_ASSETS points to the Kubernetes 1.35.0 envtest binaries.
CVK_SDWAN_LIVE=1 CVK_SDWAN_ENVTEST_LIVE=1 \
CVK_SDWAN_ENVTEST_FIXTURE=transport-trackers.yaml \
go test -tags envtest ./internal/controlleradapter/sdwan -run TestEnvtest -count=1 -v
```

For the existing `policy-profile.yaml` fixture use `TestLivePolicyProfileLifecycle`.
All examples have report-mode companion `NetworkControllerConfig` manifests.

## Implementation still required

These 87 declarations are not merely waiting for live tests. Their native mapping
and/or lifecycle implementation remains unfinished. They must not be counted as parity.

### sdwan_centralized_policies.tf

- `sdwan_activate_centralized_policy.activate_centralized_policy`
- `sdwan_centralized_policy.centralized_policy`
- `sdwan_custom_control_topology_policy_definition.custom_control_topology_policy_definition`
- `sdwan_hub_and_spoke_topology_policy_definition.hub_and_spoke_topology_policy_definition`
- `sdwan_mesh_topology_policy_definition.mesh_topology_policy_definition`
- `sdwan_vpn_membership_policy_definition.vpn_membership_policy_definition`
- `sdwan_traffic_data_policy_definition.traffic_data_policy_definition`
- `sdwan_cflowd_policy_definition.cflowd_policy_definition`
- `sdwan_application_aware_routing_policy_definition.application_aware_routing_policy_definition`

### sdwan_device_templates.tf

- `sdwan_feature_device_template.feature_device_template`
- `sdwan_attach_feature_device_template.attach_feature_device_template`

### sdwan_feature_templates.tf

- `sdwan_cedge_aaa_feature_template.cedge_aaa_feature_template`
- `sdwan_cisco_bgp_feature_template.cisco_bgp_feature_template`
- `sdwan_cisco_secure_internet_gateway_feature_template.cisco_secure_internet_gateway_feature_template`
- `sdwan_cisco_system_feature_template.cisco_system_feature_template`
- `sdwan_cisco_vpn_feature_template.cisco_vpn_feature_template`
- `sdwan_cisco_vpn_interface_feature_template.cisco_vpn_interface_feature_template`

### sdwan_features_application_priority.tf

- `sdwan_application_priority_qos_policy.application_priority_qos_policy`
- `sdwan_application_priority_traffic_policy_policy.application_priority_traffic_policy`

### sdwan_features_embedded_security.tf

- `sdwan_embedded_security_ngfw_policy.embedded_security_ngfw_policy`
- `sdwan_embedded_security_policy.embedded_security_policy`

### sdwan_features_service.tf

- `sdwan_service_routing_bgp_feature.service_routing_bgp_feature`
- `sdwan_service_dual_router_ha_feature.service_dual_router_ha_feature`
- `sdwan_service_ipv4_acl_feature.service_ipv4_acl_feature`
- `sdwan_service_lan_vpn_feature.service_lan_vpn_feature`
- `sdwan_service_lan_vpn_feature_associate_routing_bgp_feature.service_lan_vpn_feature_associate_routing_bgp_feature`
- `sdwan_service_lan_vpn_feature_associate_routing_eigrp_feature.service_lan_vpn_feature_associate_routing_eigrp_feature`
- `sdwan_service_lan_vpn_feature_associate_multicast_feature.service_lan_vpn_feature_associate_multicast_feature`
- `sdwan_service_lan_vpn_feature_associate_routing_ospf_feature.service_lan_vpn_feature_associate_routing_ospf_feature`
- `sdwan_service_lan_vpn_feature_associate_routing_ospfv3_ipv4_feature.service_lan_vpn_feature_associate_routing_ospfv3_ipv4_feature`
- `sdwan_service_lan_vpn_feature_associate_routing_ospfv3_ipv6_feature.service_lan_vpn_feature_associate_routing_ospfv3_ipv6_feature`
- `sdwan_service_lan_vpn_interface_ethernet_feature.service_lan_vpn_interface_ethernet_member_link_feature`
- `sdwan_service_lan_vpn_interface_ethernet_feature.service_lan_vpn_interface_ethernet_feature`
- `sdwan_service_lan_vpn_interface_ethernet_feature_associate_dhcp_server_feature.service_lan_vpn_interface_ethernet_feature_associate_dhcp_server_feature`
- `sdwan_service_lan_vpn_interface_ethernet_feature_associate_tracker_feature.service_lan_vpn_interface_ethernet_feature_associate_tracker_feature`
- `sdwan_service_lan_vpn_interface_ethernet_feature_associate_tracker_group_feature.service_lan_vpn_interface_ethernet_feature_associate_tracker_group_feature`
- `sdwan_service_lan_vpn_interface_gre_feature.service_lan_vpn_interface_gre_feature`
- `sdwan_service_lan_vpn_interface_ipsec_feature.service_lan_vpn_interface_ipsec_feature`
- `sdwan_service_lan_vpn_interface_svi_feature.service_lan_vpn_interface_svi_feature`
- `sdwan_service_lan_vpn_interface_svi_feature_associate_dhcp_server_feature.service_lan_vpn_interface_svi_feature_associate_dhcp_server_feature`
- `sdwan_service_route_policy_feature.service_route_policy_feature`
- `sdwan_service_ipv6_acl_feature.service_ipv6_acl_feature`

### sdwan_features_sse.tf


### sdwan_features_system.tf

- `sdwan_system_ipv4_device_access_feature.system_ipv4_device_access_feature`
- `sdwan_system_ipv6_device_access_feature.system_ipv6_device_access_feature`

### sdwan_features_topology.tf

- `sdwan_topology_custom_control_feature.topology_custom_control_feature`
- `sdwan_topology_hub_spoke_feature.topology_hub_spoke_feature`
- `sdwan_topology_mesh_feature.topology_mesh_feature`

### sdwan_features_transport.tf

- `sdwan_transport_routing_bgp_feature.transport_routing_bgp_feature`
- `sdwan_transport_ipv4_acl_feature.transport_ipv4_acl_feature`
- `sdwan_transport_route_policy_feature.transport_route_policy_feature`
- `sdwan_transport_management_vpn_feature.transport_management_vpn_feature`
- `sdwan_transport_management_vpn_interface_ethernet_feature.transport_management_vpn_interface_ethernet_feature`
- `sdwan_transport_wan_vpn_feature.transport_wan_vpn_feature`
- `sdwan_transport_wan_vpn_feature_associate_routing_bgp_feature.transport_wan_vpn_feature_associate_routing_bgp_feature`
- `sdwan_transport_wan_vpn_feature_associate_routing_ospf_feature.transport_wan_vpn_feature_associate_routing_ospf_feature`
- `sdwan_transport_wan_vpn_interface_cellular_feature.transport_wan_vpn_interface_cellular_feature`
- `sdwan_transport_wan_vpn_interface_cellular_feature_associate_tracker_feature.transport_wan_vpn_interface_cellular_feature_associate_tracker_feature`
- `sdwan_transport_wan_vpn_interface_cellular_feature_associate_tracker_group_feature.transport_wan_vpn_interface_cellular_feature_associate_tracker_group_feature`
- `sdwan_transport_wan_vpn_interface_ethernet_feature.transport_wan_vpn_interface_ethernet_member_link_feature`
- `sdwan_transport_wan_vpn_interface_ethernet_feature.transport_wan_vpn_interface_ethernet_feature`
- `sdwan_transport_wan_vpn_interface_ethernet_feature_associate_tracker_feature.transport_wan_vpn_interface_ethernet_feature_associate_tracker_feature`
- `sdwan_transport_wan_vpn_interface_ethernet_feature_associate_tracker_group_feature.transport_wan_vpn_interface_ethernet_feature_associate_tracker_group_feature`
- `sdwan_transport_wan_vpn_interface_ethernet_feature_associate_ipv6_tracker_feature.transport_wan_vpn_interface_ethernet_feature_associate_ipv6_tracker_feature`
- `sdwan_transport_wan_vpn_interface_ethernet_feature_associate_ipv6_tracker_group_feature.transport_wan_vpn_interface_ethernet_feature_associate_ipv6_tracker_group_feature`
- `sdwan_transport_wan_vpn_interface_gre_feature.transport_wan_vpn_interface_gre_feature`
- `sdwan_transport_wan_vpn_interface_gre_feature_associate_tracker_feature.transport_wan_vpn_interface_gre_feature_associate_tracker_feature`
- `sdwan_transport_wan_vpn_interface_ipsec_feature.transport_wan_vpn_interface_ipsec_feature`
- `sdwan_transport_wan_vpn_interface_ipsec_feature_associate_tracker_feature.transport_wan_vpn_interface_ipsec_feature_associate_tracker_feature`
- `sdwan_transport_ipv6_acl_feature.transport_ipv6_acl_feature`

### sdwan_inventory.tf

- `sdwan_tag.tag`

### sdwan_localized_policies.tf

- `sdwan_qos_map_policy_definition.qos_map_policy_definition`
- `sdwan_rewrite_rule_policy_definition.rewrite_rule_policy_definition`
- `sdwan_ipv4_acl_policy_definition.ipv4_acl_policy_definition`
- `sdwan_ipv6_acl_policy_definition.ipv6_acl_policy_definition`
- `sdwan_ipv4_device_acl_policy_definition.ipv4_device_acl_policy_definition`
- `sdwan_ipv6_device_acl_policy_definition.ipv6_device_acl_policy_definition`
- `sdwan_route_policy_definition.route_policy_definition`
- `sdwan_localized_policy.localized_policy`

### sdwan_network_hierarchy.tf

- `sdwan_network_hierarchy_cflowd.network_hierarchy_cflowd`
- `sdwan_network_hierarchy_security_logging.network_hierarchy_security_logging`

### sdwan_policy_groups.tf

- `sdwan_policy_group.policy_group`

### sdwan_security_policies.tf

- `sdwan_zone_based_firewall_policy_definition.zone_based_firewall_policy_definition`
- `sdwan_security_policy.security_policy`
- `sdwan_intrusion_prevention_policy_definition.intrusion_prevention_policy_definition`

### sdwan_settings.tf

- `sdwan_cloud_provider_settings.cloud_provider_settings`

### sdwan_topology_groups.tf

- `sdwan_activate_topology_group.activate_topology_group`
- `sdwan_topology_group.topology_group`

## Compatibility-blocked

`sdwan_other_trustsec_feature`: Manager 20.18 requires `sxpNodeId`, absent from the
pinned provider model; its empty device-password default is also rejected. The
compiler rejects this fixture before mutation. Fixing the compatibility mapping
and passing live tests are both still required.

## Broader deployment validation

Device attachment, policy/topology activation, operational routing/forwarding,
asynchronous recovery, pruning and import remain implementation and qualification
work. Device-impacting qualification needs a selected WAN-edge target and an
explicit restoration procedure. Object CRUD tests do not qualify these workflows.
