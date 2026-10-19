# Planned AP catalog validation and native manifest qualification

Later [physical extent preflight](planned-ap-extent-preflight.md) adds generic
coordinate checks and another passing native lifecycle. The limits below describe
this earlier qualification; controller-specific minimum-height handling remains
open.

## Implemented

CVK now reads the authenticated native maps supported-AP catalog before planned
AP execution. It validates all declared model names, selected radio bands and
antenna patterns before site geometry writes, while observing placement, and
again immediately before placement submission. A product ID is never guessed
into a map-design model alias. The bare-list catalog reader refuses partial HTTP
responses, null/enveloped lists, duplicate models, malformed selected patterns,
missing capabilities and ambiguous antenna names after normalization.

Some catalog names carry numeric choice markers. The lab listed
`1_Internal-9130-5GHz`, but the placement task rejected that label with NCMP10032
and identified `Internal-9130-5GHz` as supported. Catalog-only normalization
removes a numeric prefix followed by `_`; the manifest and outgoing antenna
value are never rewritten. A raw numbered label is not accepted as the native
pattern. Selected values may be omitted without inventing radio defaults.

The existing public sites compiler, section owner, task journal, restart
verification and transport are reused. No foundation, ND, gNOI, SWIM or physical
AP provisioning contract was changed. Catalog validation is a prerequisite,
not a durable ownership receipt or an atomic snapshot guarantee.

## Native failures that changed the implementation

The original fixture used an inventory product ID and a generic antenna name.
Read-only native catalog inspection found 102 supported map models, including
`AP9130I`, with band-specific antenna labels. The example and live fixture now
use map-model and antenna-pattern values.

Native validation returned **NCMP10038** for Z=2: placement coordinates are feet,
and this floor required Z between 3 feet and its height in feet. The corrected
example uses Z=4. Floor geometry remains in its explicitly declared units; CVK
does not reinterpret the manifest's placement coordinates as meters.
[Coordinate rejection](planned-ap-coordinate-validation-native.log).

The subsequent request received HTTP 202 and a real task, which failed with
**NCMP10032** for the catalog's numbered antenna label. Independent readback of
that exact task confirmed terminal error and an empty floor-local planned AP
inventory. This motivated the catalog-label normalization above.
[Accepted failed-task trace](planned-ap-antenna-task-failure-native.log).

## Passing native lifecycle

[Final native run](planned-ap-manifest-native.log): test **37.36 s**, package
**39.253 s**, with catalog `AP9130I`, 5 GHz, `Internal-9130-5GHz`, and Z=4 feet.

1. A single native bulk request created an isolated area/building/floor hierarchy;
   the public sites manifest reached InSync across simulated worker restarts.
2. A geometry update passed the same journal/readback/no-op checks.
3. A planned AP create returned HTTP 202 and a task. Independent native readback,
   task completion, owner retention, cleared journal and restarted no-op passed.
4. Updating X from 4.5 to 5.5 feet preserved the planned AP UUID and radio state,
   and passed the same public-manifest lifecycle checks.
5. Exact owned planned-AP UUID deletion returned a task; empty-floor baseline was
   verified. The exact owned floor, building and area were then removed, and
   hierarchy cleanup was verified.

Kubernetes persistence/worker restart is simulated by this harness. This is real
appliance qualification through the public reconciler, not installed-actor or
API-server admission qualification. No operational AP was assigned, provisioned,
renamed or configured; no switch configuration was changed.

A [second passing native cycle](planned-ap-preservation-native.log), test **33.86s**,
additionally compared the complete observed object after changing only X. The
planned AP UUID, radio UUIDs, MAC, type, Y/Z coordinates and all unselected
observed radio/antenna fields remained unchanged. Exact AP and hierarchy cleanup
passed again.

## Local verification

[Final affected race suite](planned-ap-catalog-affected.log): **10.407 s** covering
Site, PlannedAP, FloorImage and SWIM/Swim tests. New fixtures cover supported and
unsupported model/band/antenna choices, omitted selectors, malformed/partial or
ambiguous catalog rows, invalid later selections before any geometry call, and
catalog changes between planning and submission. Existing placement, image
sequencing, restart, ambiguous-acknowledgement and no-repeat fixtures pass.

## Retained diagnostic scopes and remaining work

Four earlier diagnostic hierarchies were deliberately retained by the harness:

- `Global/CVK-Q-20261015-120036-000000000`: original unacknowledged submission;
  no task receipt captured. Later empty inventory alone does not resolve it.
- `Global/CVK-Q-20261015-120653-000000000`: HTTP 400 was observed; journal retained
  Unknown without task receipt. [Trace](planned-ap-catalog-native-rejected.log).
- `Global/CVK-Q-20261015-120826-000000000`: HTTP 400/NCMP10038 coordinate rejection;
  no task receipt. [Trace](planned-ap-coordinate-validation-native.log).
- `Global/CVK-Q-20261015-121016-000000000`: accepted task subsequently failed with
  NCMP10032. [Trace](planned-ap-antenna-task-failure-native.log).

A [read-only retained-scope audit](planned-ap-retained-scopes.json) found three
sites per diagnostic hierarchy, no assigned devices and no planned APs. This does
not by itself resolve the original operations.

The passing run used a separate fresh hierarchy and cleaned it completely. No
failed/uncertain operation was replayed or automatically marked complete. The
retained scopes need separate evidence-based cleanup; they are not public prune
qualification or a reason to clear a real retained configuration journal.

Still incomplete: installed worker/admission qualification, automatic coordinate
bounds preflight, broader AP models/bands/pattern labels, native optional/default
and empty-radio cases, radio replacement, public prune/delete and broader
geometry/image/placement composition. This closes one bounded native lifecycle;
full pinned NetAsCode parity remains incomplete.

## API references

- [Cisco planned AP create schema](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/add-planned-access-points-positions/): coordinates are feet; model and antenna values come from the maps catalog.
- [Cisco planned AP readback](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/get-planned-access-points-positions/).
