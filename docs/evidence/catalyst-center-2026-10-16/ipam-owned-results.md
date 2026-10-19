# Task-correlated IPAM ownership and recovery

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.
This extends [dual-stack manifest qualification](ipam-dualstack-manifest.md).

## Implementation

New public IPAM creates publish the exact native pool/reservation UUID into the
existing action-application receipt collection atomically with Verified. The
operation uses `ipam-owned:v1:<kind>:<logical-identity-hash>`; a result receipt uses
`ipam-result:v1:<kind>:<hash>:<native-UUID>`. No new CRD fields, controller, transport,
worker, task loop or external ownership database are introduced.

The native result is accepted only from a fresh, successful correlated task
observation whose service is NCIP and progress is a bare canonical UUID. Arbitrary
prose, embedded JSON, other services, malformed IDs, failed/running/foreign/stale
and future tasks cannot establish ownership. This exact result representation
was observed on the lab appliance; other representations remain unqualified.
The UUID must independently match the selected resource's native readback,
including the existing allocation graph, site and selected IPv4/IPv6 parent and
field verification. A same-name object with a different UUID cannot satisfy a
create. Generic task validation is shared over the same observed response bytes;
product-specific progress is never interpreted by the general task observer.

Subsequent owned updates retain the UUID and replace that logical result receipt.
All selected owned pools/reservations are checked before planning, including
already-converged no-op input. Removed, renamed, replaced, foreign or ambiguously
owned resources are refused. Capacity is reserved before Prepared, including a
conservative serialized-byte bound. Immediately before native dispatch, the
existing executor also rechecks the exact Submitting operation, current source,
config/controller identity, section owner, pause/deletion state and receipt
capacity. A failed final guard consumes the plan. Owned reservations also retain
their original site UUID, including when a same-name site is replaced. Result publication uses the shared status
writer, CAS and readback. Generic Verified advancement cannot bypass this
protocol, and pending Verified release requires the exact non-pruned receipt.
Duplicate logical-key or operation receipts also prevent release.

Binding occurs at the existing IPAM executor. It traverses ordinary network,
credential/network and fabric/IPAM composition plus existing admission/foundation
wrappers. A small optional internal receipt selector lets mixed plans retain
other stages' original completion behavior. Existing custom observers are
preserved; IPAM tasks use the common task observer. Scoped known-task Unknown
recovery reuses the existing journal transition and never submits again. The
source, config owner, controller generation and original operation must remain
bound before native verification/publication.

## Compatibility

Accepted legacy `ip-pool:` / `ip-reservation:` journals retain their original
recovery path. Existing imported resources remain on the legacy update/no-op
path and do not gain an ownership receipt retrospectively. This preserves prior
behavior without treating an update as proof of resource creation ownership.
New creates and later updates of receipt-owned objects use the new protocol.
Standalone client-only compilers retain their existing protocol; public adapter
binding supplies Kubernetes ownership context.

The pinned module's primary reservation map is IPv4, augmented by same-name
IPv6 declarations. Its [provider v0.6.1 schema](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/docs/resources/ip_pool_reservation.md)
requires IPv4 subnet/prefix/parent fields; refusing IPv6-only reservations aligns
with that baseline, rather than representing an additional missing resource.

This is not automatic adoption or pruning. It does not prove recovery after loss
of all ownership status following operation retirement, arbitrary source changes
during an outstanding task, or undocumented task-result formats. Source-removal
pruning, legacy ownership migration, resize/reparent/type replacement, physical
group prerequisite composition, installed actor/API-server rollout and scale
remain separate gaps.

## Native qualification

`TestLiveIPAMDualStackManifest` passed with race detection in 51.25s (package
53.162s), using the actual Catalyst Center APIs and public config reconciler.
Kubernetes persistence and worker restarts were simulated by serialization and
fresh reconciler instances. This is not installed cluster qualification.

Generated hierarchy: `Global/CVK-Q-20261015-232100-000000000` (UTC fixture name).
Native resource UUIDs:

- IPv4 parent: `f173dd7c-7020-4572-aa7b-b6161e7ad40c`.
- IPv6 parent: `0dad99a2-f47c-4708-8a0a-8bbbe9ae22ca`.
- Dual-stack reservation: `2a91d111-ee14-4646-9f74-7141293ed711`.

| Operation | Native task |
| --- | --- |
| IPv4 parent Create | `01a141de-94ed-745c-bf35-7cca46e2136c` |
| IPv6 parent Create | `01a141de-a876-75d1-8970-c9a327774fc5` |
| Dual-stack reservation Create | `01a141de-bc13-731e-80e0-bd6c4784a95e` |
| IPv6 parent DNS Update | `01a141de-dc4c-762b-a942-7f5e23c01a46` |
| IPv6 reservation gateway Update | `01a141de-f090-7bbe-b62d-9c776d802d4e` |
| IPv4 DNS Update preserving omitted IPv6 reservation intent | `01a141df-0958-75ec-9f60-8cc8c467c9fd` |
| Exact reservation cleanup | `01a141df-2092-7407-a9c5-a839d0b6ed46` |
| Exact IPv6 parent cleanup | `01a141df-2624-7e75-8f4f-2d634a8d8ef0` |
| Exact IPv4 parent cleanup | `01a141df-278b-73ae-8d38-69e7ce3411a6` |

Each of the six acknowledged manifest tasks was deliberately restored as Unknown,
then recovered without resubmission. Each source revision retained three valid
UUID receipts; updates replaced the existing receipt instead of accumulating
entries. Native readback preserved both parent UUIDs, reservation UUID/site,
IPv6 /64, SLAAC and updated gateway across the final IPv4-only reservation change.
Fresh no-op reconcilers added no requests.

The final mutation count was three POSTs, three PUTs and three qualification
DELETEs. Exact deletion task/absence checks restored the original pool and
reservation baselines, followed by verified hierarchy cleanup. No device
assignment/provisioning or upgrade was requested. Private complete.json reports
cleaned=true; result-receipts.json contains three Verified IPAM applications.

Log: `/tmp/cvk-ipam-owned-native-final.log`.
Private source, six Accepted checkpoints, current journal, result receipts,
selected reservation readback, baselines and cleanup:
`/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-ipam-dualstack-3796853590/`.
These are host-local evidence, not tracked repository artifacts. Credential
values and tokens are excluded.

## Regression evidence and remaining qualification

Public fixtures cover new ownership, legacy accepted-task recovery, owned
updates, no-op, schema-pruned release refusal and same-name native replacement.
Unit cases cover owner/duplicate/result/protocol corruption, count/byte capacity,
wrong task UUID, wrong site/parent/family, malformed/opaque progress, and pending
or failed task results. Composition tests exercise real fabric/IPAM compilation,
credential/network and admission/foundation wrappers while preserving unrelated
observers. Full public mixed metadata-credential/IPAM and grouped fabric/IPAM cycles pass
with race detection (4.005s). They check restart/recovery, grouped journal
identity, atomic receipts, unchanged credential results, no-op and replaced-UUID
refusal. Count/serialized-byte capacity checks also pass (2.116s).

The core affected/composition race suite passed (5.120s). Final focused IPAM
regressions including duplicate-result release, final dispatch guards, original
site identity and pure/mixed/legacy cycles pass (5.120s). Late source/owner/
capacity/task/resource/pause races are separately fixture-tested (2.997s). The
final site-retention check was added after the native build; its stable/refusal
paths pass focused tests and are included in the final full-suite rerun.
ND/provider/controller regression checks pass. Strict documentation build and `git diff --check` pass. The first full suite failed in existing gateway/IPAM fixtures that supplied
short placeholder native IDs and task responses without result progress. Those
fixtures now model the canonical NCIP UUID results observed on the appliance,
while preserving logical write-order assertions, lost acknowledgements, secondary
pool, dual-stack and bulk cases. All gateway scenarios pass (74.343s); no production
identity validation was weakened. The final full Catalyst Center race suite passes in 612.966s
(`/tmp/cvk-ipam-owned-package-final.log`), covering the final production dispatch,
site identity, receipt/recovery and corrected gateway fixtures. The separately
tagged API-server tests also pass; their shared bootstrap changes were added
after that ordinary-suite build and do not enter its build tag.

A separate Kubernetes 1.35 envtest now passes real status-schema preservation,
atomic result publication/readback, stale-writer CAS conflict, Unknown retention,
missing-receipt release refusal and verified operation retirement. It covers
ordinary network_settings and grouped fabric result namespaces. The existing
SSID history admission test also passes after sharing the ephemeral API bootstrap.
The combined race-tagged envtest package passed in 13.160s; log
`/tmp/cvk-ipam-owned-envtest-qualified.log`. Its task evidence is synthetic: it
attests status/publisher semantics, not native tasks or installed actors. Initial
fixture failures were traced to Go timezone normalization; receipts now derive
from the API-normalized operation and comparisons use Kubernetes semantic time
equality. Production publisher equality was not weakened.

Shared controller-configuration/maintenance/mutation-guard regressions pass
(34.086s / 6.578s / 2.718s), as do gNOI and IOS-XE software-lifecycle regressions
(cached / 1.501s). No live upgrade was executed by these fixture tests.

Positive native mixed credential/fabric ownership, actual hardware services,
installed worker/full API-server reconciler behavior, public deletion/adoption and complete pinned
NetAsCode resource parity remain unqualified. These results do not make the
branch merge-ready or the entire model supported.

Subsequent [owned source-pruning qualification](ipam-owned-pruning.md) adds
whole-reservation/global-pool removal and explicit reintroduction for pure-IPAM
configs. Its bounded deletion semantics supersede that portion of this historical
backlog; mixed/CR-deletion cleanup and installed rollout remain pending.
