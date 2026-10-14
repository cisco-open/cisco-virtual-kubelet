# SD-WAN controller deployment

The independent `sdwan` adapter uses the existing CVK `NetworkController` worker
and `NetworkControllerConfig` intent APIs. It calls SD-WAN Manager directly;
the NetAsCode module and provider are source references for porting the data model,
schemas, defaults and API behavior. Terraform is not a build, test or deployment
dependency of the SD-WAN integration.

This implementation is a **fixture-qualified increment, not full NetAsCode
parity**. The pinned upstream module has 226 resource declarations. 181 resource declarations have fixture-level native live coverage on Manager 20.18.2.2:

| Fixture | Qualified resources | Qualification |
|---|---|---|
| `examples/configs/sdwan/banner.yaml` | Classic Cisco banner feature template | Create, read, update, cleanup, Kubernetes reconciliation |
| `examples/configs/sdwan/system-profile.yaml` | System feature profile, system banner parcel, unattached configuration group | Dependency order, create, read, cleanup, Kubernetes reconciliation |
| `examples/configs/sdwan/policy-objects.yaml` | All 24 classic policy-object collections | Create, read, update, cleanup |
| `examples/configs/sdwan/network-hierarchy.yaml` | Groups and regions at levels 0–2, and an unassigned site | Dependency order, create, read, cleanup |
| `examples/configs/sdwan/classic-bgp.yaml` | Cisco BGP feature template with AS number and IPv4 network | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-sig.yaml` | Secure internet gateway feature template with an `other` GRE tunnel | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-aaa.yaml` | cEdge AAA feature template with local authentication order | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-ethernet-interface.yaml` | Cisco Ethernet interface feature template with interface name, IPv4 address and shutdown | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-vpn.yaml` | Cisco VPN feature template with VPN ID, name and OMP distance | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-device-template.yaml` | Unattached classic device template with resolved system and banner feature templates | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-system.yaml` | Cisco system template with variable hostname, system IP and site ID | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/network-hierarchy-collectors.yaml` | Global cflowd and security-logging collector settings | Native create, read, update, cleanup, Kubernetes reconciliation |
| `examples/configs/sdwan/feature-profiles.yaml` | Eight other profile containers and implicit application-priority policy settings | Create, read, changed-value update, cleanup |
| `examples/configs/sdwan/system-features.yaml` | CLI config; system basic, BFD, global, OMP, NTP, logging, MRF, flexible port speed, performance monitoring, security | Create, read, update, cleanup |
| `examples/configs/sdwan/peripheral-features.yaml` | Transport GPS and other-profile UCSE | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/thousandeyes-feature.yaml` | Unattached ThousandEyes parcel using a token variable | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/snmp-feature.yaml` | SNMP nested views, communities, groups, users and targets | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/aaa-feature.yaml` | AAA users, RADIUS/TACACS and accounting/authorization rules | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-templates.yaml` | Classic CLI, global settings and multicast templates | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-cellular.yaml` | Classic cellular controller and cellular profile templates | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-bfd.yaml` | BFD color rows and variable intervals | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-igmp.yaml` | IGMP interfaces and nested join groups | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-ntp.yaml` | NTP authentication-key variables, trusted keys and servers | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-omp.yaml` | OMP protocol lists and conditional external OSPF routes | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-pim.yaml` | PIM interfaces and rendezvous-point addresses | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-applications.yaml` | Classic ThousandEyes and security application hosting | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-dhcp.yaml` | DHCP address ranges, static leases and option rows | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-features.yaml` | Service DHCP, endpoint/object trackers and both tracker-group types | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/transport-cellular.yaml` | Transport cellular controller and profiles with/without authentication | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-ipsec.yaml` | Classic IPsec nested IKE/IPsec settings and key variable | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-snmp.yaml` | Classic SNMP nested OIDs, communities and users | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-logging.yaml` | Classic logging TLS profile and IPv4/IPv6 servers | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-gre.yaml` | Classic GRE with conditional ingress/egress ACL variables | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-security.yaml` | Classic security with key chains and authentication translation | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-ospf.yaml` | Classic OSPF areas, interfaces, ranges and policy variable | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/routing-ospf.yaml` | Service OSPF, service OSPFv3 IPv4/IPv6, transport OSPF | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/transport-trackers.yaml` | Transport IPv4/IPv6 trackers and tracker groups | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-sig-credentials.yaml` | Global Umbrella credentials using variables | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/cisco-sse.yaml` | Cisco SSE parcel | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-svi.yaml` | Classic SVI interface template | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-cellular-interface.yaml` | Classic cellular VPN interface template | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-switchport.yaml` | Classic switchport template | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/routing-eigrp.yaml` | Service EIGRP parcel | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-switchport.yaml` | Service switchport parcel | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-multicast.yaml` | Service multicast parcel | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/system-device-access.yaml` | System IPv4 and IPv6 device-access parcels | Native lifecycle, Kubernetes reconciliation; prefix references local only |
| `examples/configs/sdwan/topology-mesh.yaml` | Topology mesh with hierarchy site-group expansion and UUID targeting | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/topology-hubspoke.yaml` | Topology hub-and-spoke with nested hub preferences and site-group expansion | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/topology-custom-control.yaml` | Custom control policy with a route rule and hierarchy site UUID targeting | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/topology-group.yaml` | Topology group bound to a declared topology feature profile | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/policy-group.yaml` | Policy group bound to application-priority and SSE profiles | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/application-priority-qos.yaml` | Application-priority QoS policy with interface targeting and an empty scheduler map | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/application-priority-traffic.yaml` | Application-priority traffic policy with VPN target and basic route-free sequence | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/embedded-security-ngfw.yaml` | NGFW policy with a source-prefix pass rule | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/embedded-security-policy.yaml` | Embedded-security assembly referencing NGFW policy with a self-to-untrusted zone pair | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-ipv4-acl-definition.yaml` and `classic-ipv6-acl-definition.yaml` | Localized classic IPv4/IPv6 ACL definitions with direct prefix rules | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-ipv4-device-acl-definition.yaml` and `classic-ipv6-device-acl-definition.yaml` | Localized device ACL definitions with source prefix and supported destination port | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-centralized-custom-control.yaml` | Centralized custom-control definition with OMP-tag route match | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-centralized-app-route.yaml` | Centralized application-aware routing definition with source-prefix match and logging action | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-centralized-traffic-data.yaml` | Centralized traffic-data definition with IPv4 source-prefix match | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-security-ips.yaml` | Classic zone firewall and intrusion-prevention definitions with resolved zone reference and unattached security-policy assembly | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-centralized-cflowd.yaml` | Centralized cflowd definition with collector and flow settings | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-centralized-membership-mesh.yaml` | Centralized VPN membership, mesh and hub-and-spoke definitions plus unattached policy assembly, with resolved site and VPN policy lists | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-qos-rewrite-definitions.yaml` | Localized QoS map and rewrite-rule definitions plus feature-policy assembly, with resolved class-map and definition references | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/classic-route-policy-definition.yaml` | Localized route policy definition with OMP-tag match and metric action | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/ipv4-acls.yaml` | Service and transport IPv4 ACL parcels | Native lifecycle, Kubernetes reconciliation; policy-object references local only |
| `examples/configs/sdwan/ipv6-acls.yaml` | Service and transport IPv6 ACL parcels | Native lifecycle, Kubernetes reconciliation; policy-object references local only |
| `examples/configs/sdwan/route-policies.yaml` | Service and transport route-policy parcels | Native lifecycle, Kubernetes reconciliation; policy-object references local only |
| `examples/configs/sdwan/transport-management-vpn.yaml` | Transport management VPN with DNS, host mapping, and static routes | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/transport-management-interface.yaml` | Transport management VPN Ethernet interface with static IPv4/IPv6, secondary address, ARP, and MTU | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-lan-vpn.yaml` | Service LAN VPN core ID, description, OMP distance, DNS, host mapping and static routes | Native lifecycle, Kubernetes reconciliation; NAT, advertisements and route leaking pending |
| `examples/configs/sdwan/service-dual-router-ha.yaml` | Dual-router HA with resolved service LAN VPN reference | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-lan-vpn-ospf-association.yaml` | Service LAN VPN to OSPF association | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/transport-wan-vpn-ospf-association.yaml` | Transport WAN VPN to OSPF association | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-lan-vpn-eigrp-association.yaml` | Service LAN VPN to EIGRP association | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-lan-vpn-multicast-association.yaml` | Service LAN VPN to multicast association | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-lan-vpn-ospfv3-associations.yaml` | Service LAN VPN to OSPFv3 IPv4 and IPv6 associations | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/service-lan-vpn-ethernet.yaml` and `service-lan-vpn-ethernet-dhcp-association.yaml` | Service LAN VPN Ethernet interface and DHCP association | Native lifecycle, Kubernetes reconciliation; advanced attributes and device activation pending |
| `examples/configs/sdwan/service-lan-vpn-ethernet-tracker-association.yaml` and `service-lan-vpn-ethernet-tracker-group-association.yaml` | Ethernet-to-tracker and tracker-group associations | Native lifecycle, Kubernetes reconciliation; device activation pending |
| `examples/configs/sdwan/service-lan-vpn-gre.yaml`, `transport-wan-vpn-gre.yaml`, and `transport-wan-vpn-gre-tracker-association.yaml` | Service and transport GRE interfaces plus transport tracker association | Native lifecycle, Kubernetes reconciliation; advanced GRE branches and device activation pending |
| `examples/configs/sdwan/service-lan-vpn-ipsec.yaml`, `transport-wan-vpn-ipsec.yaml`, and `transport-wan-vpn-ipsec-tracker-association.yaml` | Service and transport IPv4 IPsec interfaces plus transport tracker association | Native lifecycle, Kubernetes reconciliation; advanced IPsec branches and device activation pending |
| `examples/configs/sdwan/transport-wan-vpn-ethernet.yaml` and four `transport-wan-vpn-ethernet-*-association.yaml` fixtures | WAN Ethernet interface and IPv4/IPv6 tracker associations | Native lifecycle, Kubernetes reconciliation; tunnel attributes and device activation pending |
| `examples/configs/sdwan/transport-wan-vpn-cellular.yaml` and its tracker association fixtures | WAN cellular interface and IPv4 tracker/group associations | Native lifecycle, Kubernetes reconciliation; tunnel attributes and device activation pending |
| `examples/configs/sdwan/service-lan-vpn-bgp-association.yaml` and `transport-wan-vpn-bgp-association.yaml` | Service and transport BGP parcels with VPN associations | Native lifecycle, Kubernetes reconciliation; IPv4/IPv6 neighbor and advertised-network fixtures passed; redistribution and device activation pending |
| `examples/configs/sdwan/service-lan-vpn-ethernet-member.yaml` and `transport-wan-vpn-ethernet-member.yaml` | Service and transport Ethernet port-channel member links | Native lifecycle, Kubernetes reconciliation; port-channel parent and device behavior pending |
| `examples/configs/sdwan/service-lan-vpn-svi.yaml` | Service LAN VPN SVI with IPv4/IPv6, ARP and MTU | Native lifecycle, Kubernetes reconciliation; VRRP and ACL pending |
| `examples/configs/sdwan/service-lan-vpn-svi-dhcp-association.yaml` | SVI-to-DHCP server association through nested LAN VPN path | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/transport-wan-vpn.yaml` | Transport WAN VPN with ECMP, NAT64, TE service, and static routes | Native lifecycle, Kubernetes reconciliation |
| `examples/configs/sdwan/zscaler-sse.yaml` | Zscaler SSE parcel | Native lifecycle, Kubernetes reconciliation |

The baseline is NetAsCode module v1.5.0 at
`5fbf037ea57368c7e6e77694c81330b6f7b16666` and provider v0.11.5 at
`d3194b612c3f24421ec1727b6fa6e9c0bc98b07f`. The provider is
`CiscoDevNet/sdwan`; `netascode/terraform-sdwan-nac-sdwan` is the module.
`internal/controlleradapter/sdwan/testdata/upstream-lock.json` records every
resource declaration and distinguishes fixture-qualified coverage from pending
coverage. Qualification applies to the fixture's attributes, not every attribute
of that resource type.

An additional 37 declarations have native implementations but remain unqualified:
the policy-object profile and all 32 modern policy-object collections, system
CA certificates, custom applications (the lab lacks SD-AVC), device tags awaiting a selected router, and cloud-provider settings (the lab already has the singleton). SIG credential creation now passes after fixing the global flag
and variable-secret payloads.
7 declarations remain pending and TrustSec remains compatibility-blocked.
See the [implementation and live-test backlog](sdwan-live-test-backlog.md) for the
complete list, fixture paths and prerequisites.

## Model contract

The public model remains the upstream `sdwan` document. The format is
`netascode-sdwan`, model version `1.5.0`. The compiler currently supports:

- `edge_feature_templates.banner_templates`: name, description, device types,
  login/MOTD literals and variables, including upstream device-type defaults.
- `edge_feature_templates.cli_templates`, `global_settings_templates`,
  `multicast_templates`, `cellular_controller_templates` and
  `cellular_profile_templates`, `bfd_templates`, `igmp_templates`, `ntp_templates`,
  `omp_templates`, `pim_templates`, `thousandeyes_templates`,
  `secure_app_hosting_templates`, `dhcp_server_templates`, `ipsec_interface_templates`,
  `snmp_templates`, `logging_templates` and `gre_interface_templates`, including per-family device defaults and overrides.
  Generate their metadata with `scripts/sdwan/generate-classic-definitions.py
  MODULE_CHECKOUT PROVIDER_CHECKOUT`.
- `feature_profiles.system_profiles`: profile name/description, banner, basic,
  BFD, global, OMP, NTP, logging, MRF, flexible port speed, performance monitoring, security, SNMP and AAA.
  `feature_profiles.cli_profiles.config` supports native CLI parcels.
  Scalar and nested-list definitions for basic/global/OMP/NTP/logging/security are generated
  from pinned module mappings and provider defaults/value schemas. Regenerate with
  `scripts/sdwan/generate-parcel-definitions.py MODULE_CHECKOUT PROVIDER_CHECKOUT`.
  Generated metadata retains the upstream MPL-2.0 license in `definitions/LICENSE`.
- `feature_profiles.transport_profiles.gps_features` and `other_profiles.ucse` /
  `other_profiles.thousandeyes`, including the NaC GPS enable default and the
  ThousandEyes virtual-application wrapper. These fixtures are unattached.
- `feature_profiles.service_profiles.dhcp_servers`, `ipv4_trackers` and
  `object_trackers`, `ipv4_tracker_groups` and `object_tracker_groups`, with native
  name-based parcel management and full generated
  API-data schema checks for these resources.
- `feature_profiles.transport_profiles.cellular_controllers` and
  `cellular_profiles`, including authentication switches, credential variables and
  network slicing on the qualified 20.18 Manager. Inactive authentication fields
  and invalid slicing combinations are rejected before mutation.
- `configuration_groups`: name/description and references to declared system,
  transport, service, CLI or other profiles. Device associations are not enabled.
- Name/description mappings for the other eight profile collections, plus the
  automatically created application-priority settings parcel and visibility flags.
- All 24 classic `policy_objects` collections, including named SLA probe references.
  Policy names are limited to 32 characters because this Manager rewrites longer names.
- `application_catalog.custom_applications`: custom names, server names, L3/L4 rows
  and business relevance; the lab cannot qualify create until SD-AVC is enabled.
- `network_hierarchy` groups/regions through levels 0–2 and unassigned sites with
  the 20.18 location/GPS model. Global collectors remain unsupported.

`deployment.yaml` combines the baseline fixtures under one config and five
managed sections. Use `deployment-config.yaml` with its resolved output instead
of installing individual configs that would compete for the same section Lease.
The combined fixture includes hierarchy prerequisites for region lists. For
`policy-objects-config.yaml`, resolve `policy-deployment.yaml` so that the region
prerequisite is included. `system-features-config.yaml` uses `system-features.yaml`
as an alternative feature-profile section owner. The
compiler allows up to 512 resources, bounded to 128 per section by journal capacity.

Unknown fields and unsupported sections fail before mutation. Credentials and
intent Secret injection are separate concerns: endpoint credentials work through
projected files; intent `secretRefs` are currently rejected rather than ignored.
No controller-specific API payloads appear in the public CRD.

Resolve the qualified defaults outside the worker before declaring the source
resolved. This retains upstream field names:

```bash
go run ./cmd/cvk-sdwan-resolve examples/configs/sdwan/banner.yaml > /tmp/sdwan-banner.json
go run ./cmd/cvk-sdwan-resolve examples/configs/sdwan/system-profile.yaml > /tmp/sdwan-system-profile.json
```

## Deploy the qualified fixtures

Install a matching CVK image and Helm chart. The chart adds the dedicated
`cisco-virtual-kubelet-sdwan-worker` ClusterRole. Its namespace-scoped binding
grants status updates and section Lease get/create/update; it grants no Secret
API access, device mutation permission, or remote-deletion permission.

Create the `sdwan-lab` namespace and a same-namespace `sdwan-credentials` Secret
with `username` and `password` keys using your existing secret-management process.
Do not put credentials into the model or commit them to the repository.

```bash
kubectl create namespace sdwan-lab
kubectl apply -f examples/configs/sdwan/controller.yaml
kubectl -n sdwan-lab create configmap sdwan-banner-fixture \
  --from-file=model.yaml=/tmp/sdwan-banner.json
kubectl -n sdwan-lab create configmap sdwan-system-profile-fixture \
  --from-file=model.yaml=/tmp/sdwan-system-profile.json
kubectl apply -f examples/configs/sdwan/banner-config.yaml
kubectl apply -f examples/configs/sdwan/system-profile-config.yaml
kubectl -n sdwan-lab get networkcontrollers,networkcontrollerconfigs
```

The examples initially report drift. Select `spec.mode: apply` to deploy the
fixtures. Expect incremental `Applying` status followed by `InSync` after
read-back verification. Reconciliation preserves existing matching resources
without resubmitting their creation. An external object with the same name is a
collision, not implicit authorization to adopt it.

The lab manifest explicitly opts into insecure TLS because this endpoint's
self-signed certificate has no IP SAN. Other deployments should supply a valid
certificate and trust bundle. Sessions remain in process memory, rotate with
projected material, and have bounded lifetimes. Redirects are rejected; only GET
may refresh and retry after 401. Error bodies, cookies and tokens are not logged.

## Ownership and recovery

Ownership is per controller UID, global scope and managed section. One config
incarnation owns each section; separate configs cannot split the same section.
Each worker session holds a 90-second Lease. Before a remote mutation, the worker
persists a `Dispatched` receipt and rechecks the live endpoint/config/source and
Lease bindings. A successful response persists the remote ID before verification.

A settled Lease can transfer to a replacement worker for the same config after
expiry. An unresolved dispatch blocks takeover and automatic replay. Such a
failure requires investigation; an automatic uncertain-outcome recovery workflow
is not implemented. Status reports `OutcomeUnknown` or `SectionOwned` without
raw controller payloads. Never clear the journal merely to retry a timed-out POST.

`Retain` is the supported prune/deletion policy. Deleting intent retains remote
objects and its ownership journal; deletion does not transfer ownership to a new
config UID. Remote pruning, explicit adoption/import, ownership release and
device attachment/activation are still pending. The fixture test harness cleans
up only its own created IDs in reverse dependency order.

## Native CVK tests

Normal tests do not contact the lab. Live tests require explicit environment
flags and credentials supplied outside Git. The test harness creates temporary
credential projections and removes them at completion.

```bash
go test ./internal/controlleradapter/...
go test -race ./internal/controlleradapter/... ./cmd/cisco-vk
# KUBEBUILDER_ASSETS must point to Kubernetes 1.35.0 test binaries.
go test -tags envtest ./internal/controlleradapter/sdwan -run TestEnvtest -v
```

Live native lifecycle tests use `CVK_SDWAN_LIVE=1` and
`CVK_SDWAN_MUTATE=1`, with `SDWAN_URL`, `SDWAN_USERNAME`, `SDWAN_PASSWORD`
and the appropriate TLS setting supplied outside Git. Select the intended
`TestLive*Lifecycle` test explicitly. Live Kubernetes tests additionally use
`CVK_SDWAN_ENVTEST_LIVE=1` and select a fixture through `CVK_SDWAN_ENVTEST_FIXTURE`.
These tests exercise CVK's native compiler, API client and reconciliation.

## Remaining deployment parity

The remaining resource mappings, full upstream schema/default coverage, classic
device templates, routing and transport/service features, live qualification of modern
policy-object parcels, policy definitions, global hierarchy collectors, topology/policy groups, settings, device variables,
attachment/deployment, asynchronous activation recovery, pruning and import are
not complete. Reproducible device-level qualification also needs explicit WAN-edge
targets and a restoration policy. Inventory visibility does not authorize choosing
an arbitrary existing router as the test target.

See [the qualification record](../evidence/sdwan-2026-10-14/README.md) for measured
results and limits.

### Manager 20.18 TrustSec incompatibility

The pinned provider's TrustSec payload is rejected because `data.sxpNodeId` is
required by this Manager but absent from the provider model. Its default empty
`devicePassword` is also rejected. The reproducible fixture is under
`examples/configs/sdwan/unqualified/trustsec.yaml`; the compiler rejects TrustSec
before mutation. Generated TrustSec metadata is retained for further compatibility
work and does not count toward qualified coverage.

### Policy-object profile qualification

`policy-profile.yaml` defines the singular policy-object profile and the original
fifteen policy parcels. `policy-prefixes.yaml`, `policy-dependencies.yaml` and
`policy-security.yaml` exercise the remaining seventeen modern policy-object
declarations. All have passed local compiler and dependency tests. Live qualification is pending: Manager already has its singleton
policy-object profile and rejects creation of another. Existing resources were
not adopted or changed. The inventory marks these definitions
`implemented-unqualified`.
A read-only-parent workflow or an empty Manager is needed for live qualification.

The pinned module uses `forwarding_classes` where the current documentation calls
this collection `class_maps`. This branch follows the pinned module's name.
NaC integer queue and TLOC preference values are encoded as strings for the
provider API, with their public numeric constraints retained.

The pinned module's SNMP `groups[].view_variable` expression incorrectly traverses
`group.view.view_variable`. That unavailable input is explicitly rejected.
AAA `accounting_rules[].start_stop_variable` is passed verbatim by the module and
therefore requires the upstream `{{variable}}` expression. Other generated parcel
variable fields wrap the public variable name as required by their module mapping.

### Classic PIM provider limitation

The pinned provider does not serialize `pim_templates[].rp_addresses[].optional`.
CVK rejects this field before mutation; the qualified fixture omits it. Other PIM
row types preserve their optional flag and provider-defined priority order.

### Tracker references and classic logging compatibility

Tracker groups require two distinct tracker names declared in the same service
profile. CVK creates trackers before groups, resolves controller IDs before every
write and comparison, and rejects missing dependencies without sending a mutation.

The pinned module reads `version_ciphersuites_variablevariable` for classic logging
TLS ciphersuite variables. CVK rejects that typo and `ciphersuites_variable`; literal
`ciphersuites` is fixture-qualified. Classic IPsec supports nested `ike`/`ipsec`
fields and the numeric DH-group form shown in the NaC example.
