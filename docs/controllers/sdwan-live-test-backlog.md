# SD-WAN implementation and live-test backlog

This is a progress inventory, not a claim of completed deployment parity. The pinned
NetAsCode v1.5.0 module contains 226 resource declarations. Current status:

| Status | Declarations |
|---|---:|
| Fixture-qualified against live Manager | 218 |
| Implemented with local fixtures; live tests outstanding | 7 |
| Implementation still pending | 0 |
| Compatibility-blocked (TrustSec) | 1 |

Configuration-group and policy-group device association/deployment now compile from site routers, including scalar/list group variables, topology labels and deploy flags. Their Manager associate/variables/deploy/readback/update workflow passes a local HTTP fixture. Both group association types passed live create/read/disassociate/delete on BR-2-WAN after confirming it had no existing group assignment. Variable upload and deployment remain local-API-tested; live device push still needs a configuration fixture preserving its running system and transport settings. Device-template attachment now compiles from site routers with `device_template` and `device_variables`; its Manager config-input/attach/readback/update workflow and Adaptive QoS variable-name lookup pass local HTTP fixtures. Live attachment remains untested; the selected BR-2-WAN has no classic template, but the current fixture carries placeholder system identity and cannot safely be pushed as-is. Topology-group `activate: true` now compiles as a single activation ordered after its group and passes a local API/task lifecycle test; a live attempt with an isolated site and mesh policy reached the asynchronous task, which failed because the vSmart control component has neither an attached device template nor a control-component setting. Centralized policy activation now compiles from `activated_policy`, orders after its declared feature policy, and passes a local API/task lifecycle test. A live attempt was rejected with `POLICY0006`: vSmart `10.255.255.2` is not in vManage mode. Both activation fixtures were removed, and a later Manager inventory showed zero centralized policies and zero topology groups. An unattached classic Cisco BGP template with AS number, router ID and an IPv4 network passed native lifecycle; nested neighbor and route-policy compilation passed local checks. Provider-missing optional-variable fields and the module compare-router-ID variable typo remain explicitly recorded. An unattached classic secure internet gateway template with an `other` GRE tunnel passed native lifecycle. Provider/tunnel selector derivation and HA-pair compilation have local checks; Umbrella, Zscaler and IPsec variants remain outside the live fixture. An unattached classic cEdge AAA template with local authentication order passed native lifecycle; rule indexing and nested RADIUS-client VPN compilation passed local checks. Other AAA branches remain outside the live fixture. An unattached classic Cisco Ethernet interface template with interface name, IPv4 address and shutdown passed native lifecycle. Its complete generated attribute model remains much broader than the live fixture. Classic Cisco VPN fixtures with ID/name/OMP distance and IPv4/IPv6 DNS servers plus flattened host entries pass native lifecycle and Kubernetes reconciliation. OMP OSPF and global route import/export external subtypes now pass native lifecycle; other route branches remain outside these fixtures. Unattached classic device templates now pass native lifecycle and Kubernetes reconciliation with system/banner references, a nested service VPN containing BGP and Ethernet templates, and localized/security policy bindings. Transport, management and service VPN nesting now passes live lifecycle with Ethernet children, system logging/NTP children and service Ethernet DHCP. Switchport and cellular controller/profile references also pass live lifecycle. Other VPN child families remain outside the live fixtures. The security container-profile reference is live-qualified. NetAsCode site/router tags now compile and preserve existing overlapping tags in a local association test. Tag create, BR-2-WAN association, readback, removal, and cleanup passed live; its prior empty tag state was restored. The classic Cisco system template now has an unattached fixture covering hostname, system IP and site-ID variables, and passed native lifecycle plus Kubernetes reconciliation. Provider-missing endpoint tracker fields and the schema-missing EPFR field remain explicitly rejected. The cloud-provider settings model and local API lifecycle are implemented, but live qualification is outstanding because the lab already contains that global singleton with potentially real credentials. The latest live-qualified increment covers Global network-hierarchy cflowd and security-logging collector settings with native create/read/update/delete and Kubernetes reconciliation. The lab had no pre-existing singleton settings, and both were removed after qualification. The preceding increment qualifies a classic centralized custom-control definition with OMP-tag route match and default reject action. Site-targeted binding and other actions remain outside the fixture. The preceding increment qualifies a classic centralized application-aware routing definition with a source-prefix match and log action. Other SLA-class, match and action variants remain outside the fixture. The preceding increment qualifies a classic centralized traffic-data definition with a source-prefix rule and default drop action. Other match/action variants remain outside the fixture. The preceding increment qualifies a classic zone-based firewall definition with a source-prefix pass rule and resolved self-to-zone pair, plus its security-policy reference. The preceding increment qualifies an unattached classic security-policy assembly with a resolved intrusion-prevention definition through native lifecycle and Kubernetes reconciliation. Manager returns an empty security-policy POST body, so CVK finds the created ID in the list response. The preceding increment qualifies a classic intrusion-prevention definition with detection mode, a balanced signature set, warning logging and target VPN through native lifecycle and Kubernetes reconciliation. The preceding increment qualifies a classic centralized hub-and-spoke definition with separate hub and spoke site lists and includes it in the unattached centralized feature-policy assembly. The preceding increment qualifies an unattached centralized feature-policy assembly with resolved mesh and VPN-membership definitions through native lifecycle and Kubernetes reconciliation. Activation, target routers and other definition families remain outside the fixture. The preceding increment qualifies a classic centralized cflowd definition with a collector. Manager rejects the provider-enumerated `exportSpread: disable`, so that optional variant remains unqualified. The preceding increment qualifies classic centralized VPN-membership and mesh-topology definitions with resolved site and VPN policy lists through native lifecycle and Kubernetes reconciliation. Other centralized definition families and activation remain outside the fixture. The preceding increment qualifies the localized feature-policy assembly with resolved QoS-map and rewrite-rule definitions through native lifecycle and Kubernetes reconciliation. Other definition families and settings remain outside the fixture. The preceding increment qualifies localized QoS map and rewrite-rule definitions with a resolved classic class-map policy object through native lifecycle and Kubernetes reconciliation. Other scheduler and rewrite variants remain outside the fixture. The preceding increment qualifies a localized route policy definition with OMP-tag match and metric action through native lifecycle and Kubernetes reconciliation. Other route-policy match and action fields remain outside the fixture. The preceding increment qualifies classic localized IPv4 and IPv6 device ACL definitions with a source prefix and supported destination port. Manager requires port 22 or 161 in each device ACL rule and normalizes the IPv6 type casing. The preceding increment qualifies classic localized IPv4 and IPv6 ACL definitions with a direct source-prefix match through native lifecycle and Kubernetes reconciliation. Other match fields, actions and policy assembly remain outside the fixtures. The preceding increment qualifies the embedded-security policy assembly with a resolved NGFW parcel and a self-to-untrusted zone pair. Advanced-inspection references, additional zone types and device deployment remain outside the fixture. The preceding increment qualifies an embedded-security NGFW policy with a source-prefix pass rule. Other match criteria, actions and inspection profiles remain outside the fixture. The preceding increment qualifies an application-priority traffic policy with VPN targeting and a basic sequence. Match entries and actions remain outside the fixture. The preceding increment qualifies an application-priority QoS parcel with interface targeting and no schedulers. Class-map references and scheduler attributes remain outside the fixture. The preceding increment qualifies an unattached policy group referencing application-priority and SSE profiles through native lifecycle and Kubernetes reconciliation. Device association and policy activation are not covered. The preceding increment qualifies a topology group referencing a CVK-created topology profile through native lifecycle and Kubernetes reconciliation. The preceding increment qualifies the custom-control topology parcel with a route rule and resolved hierarchy site UUID through native lifecycle and Kubernetes reconciliation. The preceding increment qualifies service and transport Ethernet port-channel member-link parcels using their conditional payload branches. The preceding increment qualifies service and transport BGP parcels with AS number, router ID, and one IPv4 or IPv6 neighbor and advertised network, plus their VPN associations. Redistribution and policy references remain outside these fixtures. The preceding increment qualifies a non-tunnel WAN cellular parcel and its IPv4 tracker and tracker-group associations. The pinned provider points the group association to `tracker/`, but Manager actually stores and reads it at `trackergroup/`; CVK uses the observed Manager path. The preceding increment qualifies the non-tunnel WAN Ethernet parcel and all four IPv4/IPv6 tracker and tracker-group associations through native lifecycle and live Kubernetes reconciliation. Manager requires IPv6 settings on the parent before an IPv6 tracker can be associated. The preceding increment qualifies service and transport IPsec interface parcels and a transport IPsec-to-IPv4-tracker association. Both use a variable preshared-key reference and passed native lifecycle plus live Kubernetes reconciliation. The preceding increment qualifies service and transport GRE interface parcels and a transport GRE-to-IPv4-tracker association through native lifecycle and live Kubernetes reconciliation. The preceding increment qualifies Ethernet interface associations to IPv4 trackers and tracker groups through native lifecycle and live Kubernetes reconciliation. The preceding increment qualifies a service LAN VPN Ethernet interface and its three-level DHCP-server association through native lifecycle and live Kubernetes reconciliation. The Ethernet fixture covers IPv4 addressing and ARP; NAT, port-channel, VRRP, ACL, TrustSec and other advanced branches remain outside that fixture. The preceding increment qualifies the three-level service LAN VPN SVI-to-DHCP association. The preceding increment qualified a service LAN VPN SVI child with IPv4/IPv6 addressing, ARP and MTU. VRRP, ACL references and DHCP attachment remain pending. The preceding increment qualified six service and transport VPN routing associations: OSPF, EIGRP, multicast, and service OSPFv3 IPv4/IPv6. Each resolved its parent VPN and referenced routing parcel IDs through the Manager before association. The preceding increment qualified the core service LAN VPN parent and dual-router HA with a resolved VPN reference. The LAN VPN compiler currently accepts ID, name, OMP distance, DNS, host mapping, and IPv4/IPv6 next-hop and null-route fields; NAT, advertisements, route leaking and child interfaces are still outstanding. The preceding increment qualified the transport management VPN Ethernet child and its nested Manager path. The preceding increment qualified the transport management and WAN VPN parent parcels with native lifecycle and live Kubernetes reconciliation. The fixtures cover DNS, host mappings, IPv4 and IPv6 static routes, WAN ECMP, NAT64 pools, and the TE service. Interface children and device activation remain pending. The preceding increments qualified route policies, IPv4 and IPv6 ACLs, topology mesh and hub-and-spoke, and system device access. Policy-object references compile and order locally but need a lab where CVK can create the singleton policy-object profile; this lab already has one.

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

All 32 policy-object child declarations passed native create/read/update/delete under the existing Manager policy-object profile using isolated CVK names. The original parent child-ID set was verified after cleanup for each fixture. The singleton profile itself remains unqualified: CVK does not adopt or update the existing lab profile, so its creation still needs an empty test Manager.

| Upstream declaration | Native model path (under `sdwan`) | Fixture(s) | Prerequisite |
|---|---|---|---|
| `sdwan_cloud_provider_settings` | `settings.cloud_provider_settings` | [cloud-provider-settings.yaml](https://github.com/cisco-open/cisco-virtual-kubelet/blob/pr/johalley/sdwan-support/examples/configs/sdwan/cloud-provider-settings.yaml) | Existing global settings singleton in this lab; use an isolated Manager with no production credentials for create/update qualification |
| `sdwan_custom_application` | `application_catalog.custom_applications` | [custom-applications.yaml](https://github.com/cisco-open/cisco-virtual-kubelet/blob/pr/johalley/sdwan-support/examples/configs/sdwan/custom-applications.yaml) | Enable SD-AVC on a test vManage; current Manager returns `POLICYVALIDATION0001` before creating the object |
| `sdwan_policy_object_feature_profile` | `feature_profiles.policy_object_profile` | [policy-profile.yaml](https://github.com/cisco-open/cisco-virtual-kubelet/blob/pr/johalley/sdwan-support/examples/configs/sdwan/policy-profile.yaml) | Manager permits only one policy-object profile |
| `sdwan_system_ca_certificate_feature` | `feature_profiles.system_profiles.ca_certificate` | [ca-certificate.yaml](https://github.com/cisco-open/cisco-virtual-kubelet/blob/pr/johalley/sdwan-support/examples/configs/sdwan/ca-certificate.yaml) | Replace placeholder certificate ID with an existing lab certificate |
| `sdwan_device_template_attachment` | `sites.routers.device_template` | [classic-device-attachment.yaml](https://github.com/cisco-open/cisco-virtual-kubelet/blob/pr/johalley/sdwan-support/examples/configs/sdwan/classic-device-attachment.yaml) | Capture and preserve a router's actual system/transport variables before attachment; fixture currently contains placeholder identity |
| `sdwan_centralized_policy_activation` | `centralized_policies.activated_policy` | [classic-centralized-activation.yaml](https://github.com/cisco-open/cisco-virtual-kubelet/blob/pr/johalley/sdwan-support/examples/configs/sdwan/classic-centralized-activation.yaml) | Put the lab vSmart into vManage mode; current Manager rejects activation with `POLICY0006` |
| `sdwan_topology_group_activation` | `topology_groups.activate` | [topology-group-activation.yaml](https://github.com/cisco-open/cisco-virtual-kubelet/blob/pr/johalley/sdwan-support/examples/configs/sdwan/topology-group-activation.yaml) | Attach a device template or control-component setting to the vSmart; current deployment task fails without one |

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

# On a Manager with one existing policy-object profile, child fixtures can
# be tested without creating or updating that parent. Run one fixture at a time.
CVK_SDWAN_LIVE=1 CVK_SDWAN_MUTATE=1 \
CVK_SDWAN_POLICY_FIXTURE=policy-security \
go test ./internal/controlleradapter/sdwan -run "^TestLiveExistingPolicyObjectChildren$" -count=1 -v

# On a lab whose vSmart meets the prerequisites above, select one activation
# fixture at a time. The test verifies an empty baseline and registers cleanup
# before sending the deployment request.
CVK_SDWAN_LIVE=1 CVK_SDWAN_MUTATE=1 \
CVK_SDWAN_ACTIVATION_FIXTURE=topology-group-activation \
go test ./internal/controlleradapter/sdwan -run "^TestLiveActivationLifecycle$" -count=1 -v

# KUBEBUILDER_ASSETS points to the Kubernetes 1.35.0 envtest binaries.
CVK_SDWAN_LIVE=1 CVK_SDWAN_ENVTEST_LIVE=1 \
CVK_SDWAN_ENVTEST_FIXTURE=transport-trackers.yaml \
go test -tags envtest ./internal/controlleradapter/sdwan -run TestEnvtest -count=1 -v
```

For the existing `policy-profile.yaml` fixture use `TestLivePolicyProfileLifecycle`.
All examples have report-mode companion `NetworkControllerConfig` manifests.

## Implementation still required

These 7 declarations are not merely waiting for live tests. Their native mapping
and/or lifecycle implementation remains unfinished. They must not be counted as parity.

### sdwan_centralized_policies.tf


### sdwan_device_templates.tf


### sdwan_feature_templates.tf


### sdwan_features_application_priority.tf


### sdwan_features_embedded_security.tf


### sdwan_features_service.tf


### sdwan_features_sse.tf


### sdwan_features_system.tf


### sdwan_features_topology.tf


### sdwan_features_transport.tf


### sdwan_localized_policies.tf


### sdwan_policy_groups.tf


### sdwan_security_policies.tf


### sdwan_topology_groups.tf


## Compatibility-blocked

`sdwan_other_trustsec_feature`: Manager 20.18 requires `sxpNodeId`, absent from the
pinned provider model; its empty device-password default is also rejected. The
compiler rejects this fixture before mutation. Fixing the compatibility mapping
and passing live tests are both still required.

## Broader deployment validation

Device attachment, policy/topology activation, operational routing/forwarding,
asynchronous recovery, pruning and import remain implementation and qualification
work. The optional `TestLiveActivationLifecycle` has baseline checks and a
restoration cleanup registered before activation; its current lab attempts failed
at the prerequisites recorded above. Object CRUD tests do not qualify these
workflows.
