# Dual-stack IPAM public manifest qualification

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.
Pinned NetAsCode module: 0.5.0.

## Change

IPAM submission plans now consume a request-local atomic permit before native
replanning. Concurrent calls cannot submit the same plan twice, and rejection,
missing task acknowledgements or failed native preflight do not permit another
attempt from that plan. The existing configuration journal, shared transport,
whole-allocation validation, source ownership and task/readback path remain in
use; there is no new controller or public API field.

The added `ipam-dualstack-apply.yaml` example uses two global pools with the same
reservation name and type, merged into one site-bound dual-stack reservation.
IPv6 /64 SLAAC follows the pinned model's existing behavior. The example must be
merged into the existing network_settings owner and requires an existing empty
site plus available address space. It allocates design data, not device services.

The modern API represents reservations as IPv4 with optional IPv6; see Cisco's
[reservation schema](https://developer.cisco.com/docs/catalyst-center/reserve-create-ip-address-subpools/).
Global pool type/CIDR changes remain excluded, consistent with the documented
[update restrictions](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/updates-a-global-ip-address-pool/).

## Real appliance result

`TestLiveIPAMDualStackManifest` passed in 51.87s (package 53.980s) with race
detection, the real Catalyst Center API and public config reconciliation.
Kubernetes persistence was simulated: every accepted operation was serialized
and restored before a fresh reconciler continued. It was not an installed-worker
or real Kubernetes API-server qualification.

The isolated hierarchy was `Global/CVK-Q-20261015-225833-000000000` (UTC name).
Its area/building/floor were created, read back, and removed by the existing
qualification helper. IPAM used unused benchmark/documentation ranges selected
from the generated name. No device assignment/provisioning or upgrade API was
permitted by the IPAM qualification transport.

| Stage | Task / native identity |
| --- | --- |
| IPv4 parent creation | Task `01a141ca-06bd-7332-9d5c-a1a2b02e6d00`; UUID `884aa3d4-6cfa-4e19-bbdd-d351fd93dca3` |
| IPv6 parent creation | Task `01a141ca-1a63-7ecd-b466-baeb224fbf03`; UUID `b2b381ed-91bf-498d-8cc0-ee943a89a384` |
| Dual-stack reservation creation | Task `01a141ca-2e2e-7f2f-bbc1-0a0751ff7cfe`; UUID `be8a7296-1d9d-4cce-a9cc-ba0997cd36a0` |
| IPv6 parent DNS update | Task `01a141ca-4cf1-7428-ba21-52928db07f9e` |
| IPv6 reservation gateway update | Task `01a141ca-60fc-7715-95ba-572c3ff27990` |
| IPv4-only reservation DNS revision, preserving IPv6 | Task `01a141ca-78d7-716e-8a9d-2d89dc42a257` |
| Exact reservation cleanup | Task `01a141ca-8f49-74af-88c7-cf3e8134cd38` |
| Exact IPv6 parent cleanup | Task `01a141ca-94d3-7aaf-ba32-baa5e6d8c0ee` |
| Exact IPv4 parent cleanup | Task `01a141ca-9a71-7c39-af7d-b6dbc6c82676` |

All six manifest operations retained an Accepted checkpoint. Each of three source
revisions converged through native task completion and independent readback;
restarted no-op passes did not add requests. Both parent UUIDs, the reservation
UUID, site identity and IPv6 SLAAC were retained. Removing IPv6 reservation intent
from the final source preserved its allocated parent, /64 space and updated
gateway while the IPv4 DNS change converged.

The final count was three POSTs, three PUTs and three qualification DELETEs.
Reservation removal preceded parent removal; each delete had an accepted task
and verified absence. Original global pool and reservation inventories were
restored, followed by verified hierarchy cleanup. Private `complete.json` reports
`cleaned: true` with those counts.

Log: `/tmp/cvk-ipam-dualstack-native-final.log`.
Private source, journal, six accepted operations, baselines and cleanup evidence:
`/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-ipam-dualstack-3769464170/`.
`verified-reservation.json` retains the complete final public readback, including
its preserved IPv6 prefix/gateway/SLAAC and both parent UUIDs.
Directory mode is 0700; files are 0600. These are host-local evidence, not tracked
repository artifacts. Credentials and tokens are not copied into them.

## Regression checks and limits

Affected IPAM/allocation/revalidation/fabric-composition tests and the dual-stack
example Apply/Report compilation pass with race detection (2.244s). Concurrent
submission tests cover pool Create/Update and reservation Update under accepted,
missing-acknowledgement, rejected and preflight-failure outcomes; each plan stays
consumed. ND/provider/controller regression checks pass. The final live-tag affected suite
also compiles the conservative qualification guard that retains the entire
hierarchy until all IPAM cleanup has completed; no native request is enabled by
that fixture-only regression run. The first example test
found unquoted IPv6 YAML ending in `::`; corrected quoted values pass.

The preceding full Catalyst Center suite passed before this IPAM change; the
current change is covered by the affected suite and native cycle above. It is
not a new full-package test result.

Remaining gaps include installed actor/API-server qualification, durable
resource-level creation/adoption lineage for pruning, public deletion, prefix
resize/reparent/type replacement, broader inherited
site/default combinations, allocation scale and actual device DHCP/DNS/SLAAC
behavior. Source omission preserves an existing family; it does not authorize
family deletion. An unacknowledged operation remains subject to the existing
journal recovery fence; the request-local permit is not a cross-process lock.
This evidence does not establish full NetAsCode parity or branch merge readiness.

Subsequent work adds task-correlated creation/update ownership and scoped Unknown
recovery; see [the newer qualification](ipam-owned-results.md). Its compatibility
and remaining gaps supersede the creation-lineage portion of this historical result.

Pinned-model clarification: IPv6-only reservations are not a missing capability
of this baseline. NetAsCode 0.5.0 `cc_network_settings.tf` builds the primary
reservation map from IPv4 declarations and augments it with IPv6; the
[provider v0.6.1 schema](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/docs/resources/ip_pool_reservation.md)
requires IPv4 parent/subnet/prefix fields. CVK's IPv6-only refusal therefore
aligns with the pinned contract.
