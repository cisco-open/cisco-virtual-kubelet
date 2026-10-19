# Installed source-bound floor-image assets

The [installed asset lifecycle](installed-floor-images-native.log) passed in
**138.76 s** (Go **141.191 s**) using the normal manager/worker and a ConfigMap
source from the first hierarchy creation. It qualified private area/building/floor
Create, geometry Update and original acknowledged-task process recovery before
the asset stages.

Two distinct PNG byte revisions were applied by changing only the existing
ConfigMap. Configuration generation stayed unchanged. Each installed synchronous
completion bound the exact floor UUID, ConfigMap UID/resource version and
source-byte digest, using one replaceable image receipt slot. No asynchronous
task was invented for the synchronous upload response. Three private creators
and one private latest-applied geometry receipt remained unchanged.

Removing `image_path` returned the configuration to geometry-only InSync while
retaining upload evidence. Actual Pod replacement preserved all five rows.
Retain intent deletion and independent supervised floor/building/area cleanup
restored the complete typed site baseline. The test did not execute a physical
operation or public site Delete. Image completion is a write acknowledgement,
not independent observation of the remote image bytes.

[Source/task/receipt audit](installed-floor-images-validation.json) separates
the four asynchronous geometry tasks, two synchronous image checkpoints and
three supervised cleanup tasks. [Default-off compilation](installed-floor-images-disabled-gate.log)
and [unknown-mode refusal](installed-floor-images-mode-gate.json) are preparation
checks, not substitutes for the native result. The first result above used the
previous byte-only image identity; later source-path work has a separate source
fingerprint and qualification boundary.

## Upstream contract and remaining lifecycle differences

The pinned NetAsCode 0.5.0 module emits the floor-image resource when
`image_path` is non-null and the floor is selected. In the inspected
[provider source](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/01058637dd5ed4fca93e1b2a96dd7032aec2d3a1/internal/provider/resource_catalystcenter_floor_image.go),
both `floor_id` and `source_path` require replacement. Create uploads; Read and
Delete do not call a remote API. Cisco documents the
[floor-image upload endpoint](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/uploads-floor-image/).
Native image-byte equality should therefore not be substituted for the upstream
write-only contract, nor advertised as something this test proves.

The comparison identified a concrete CVK difference: byte-only receipts treated
a different `source_path` with identical bytes as an unchanged upload. New
versioned operation identities now bind the hashed path as well as floor and
content; the stable per-floor receipt key remains unchanged. Legacy receipts
cannot establish their old filename and keep their original no-replay and
task-recovery semantics. Local and temporary-API tests pass. The updated installed
result below qualifies new v2 writes without rewriting the earlier run's source
fingerprint or historical v1 evidence.

## Path replacement: terminal v2 qualification

The [updated installed run](floor-image-v2-native.log) passed in **158.18 s**
(Go **160.618 s**) with the newly built unified nonroot worker image. It applies
two PNG byte revisions and then changes only the full model path, keeping both
the basename and bytes identical. The third source-bound synchronous upload
completes with a different path hash, preserves configuration generation and
replaces the same single per-floor receipt slot. The
[independent audit](floor-image-v2-native-validation.json) verifies the final
path hash, task/receipt counts, original-task process recovery, all-five-row Pod
restart persistence, omission retention and exact cleanup with restored typed
site baselines. The connector is Ready with no active intent or operation.

The [v2 gate/source/image record](floor-image-v2-validation.json) links the local
upload/protocol gate (3.842 s), wider private composition/handoff race command
(17.188 s) and temporary Kubernetes API command (53.375 s). Nexus Dashboard's
unchanged tests report a valid cached pass; the selected SWIM tests pass in
2.467 s. This is not a full direct-device or physical regression qualification.

At this v2 checkpoint, omission/reintroduction lifetime handling remained pending;
the separately qualified lifetime stage below closes that bounded case. Legacy path migration,
other format/size boundaries, lost-response cases, populated/physical AP behavior
and public site deletion remain separate gaps. This resource and the full
93-resource model are not marked complete.


## Declaration lifetime: omission and reintroduction

The new omission marker retags the existing single per-floor receipt slot while
preserving the original operation, acknowledgement and verification time. It
records the observed configuration generation and bound ConfigMap UID/resource
version. Source and owner checks, private custody/native floor checks and the
status compare-and-swap precede publication. No remote image Delete or invented
operation is inferred. A later declaration of the same path and bytes requires a
fresh operation bound to a later source snapshot; it replaces the same slot.

The [current source and gate record](floor-image-omission-validation.json)
reports the race-enabled local regression command (18.535 s) and eight isolated
Kubernetes API scenarios (12.967 s). Cases include omission/null/legacy lifetimes,
two repeated cycles, stale source, foreign ownership, pending operations, replaced
floor identities and status conflicts. These gates are distinct from installed
native qualification.

The [first installed attempt](floor-image-lifetime-first-attempt.json) completed
one same-path/same-byte reintroduction, then failed a harness classification check:
the old prefix filter counted a retained image marker as a geometry row. The
harness now uses the shared strict receipt decoder for both active and retained
rows. Separate [supervised retained-fixture cleanup](floor-image-lifetime-recovery-cleanup.log)
passed in 6.51 s (Go 8.931 s), preserving the unrelated current baseline. Cleanup
does not convert the failed attempt into a passing lifecycle qualification.


### Terminal installed lifetime qualification

The [fresh corrected run](floor-image-lifetime-native.log) passed in **198.14 s**
(Go **200.490 s**) using image
`cvk-catc-worker-qualification:20261017-image-lifetime-44ace7027833`.
It performs three source-bound synchronous upload revisions followed by **two
same-path/same-byte omission/reintroduction cycles**. Both reintroductions get
fresh operation IDs and current ConfigMap revisions without changing the CR
generation. The three creator rows and latest geometry row remain unchanged;
the single image slot is replaced rather than appended. The two omission
markers preserve their preceding original operation and verification time.

Original acknowledged geometry-task recovery, later actual Pod replacement,
Retain intent retirement and supervised child-first cleanup all pass. The
[terminal source/image/runtime audit](floor-image-lifetime-native-validation.json)
confirms five retained rows, one image slot, no active configuration intent or
operation and a Ready connector. The typed native site baseline was restored.
The source hashes match the qualified code; the first failed attempt remains a
separate failed result.

This closes the installed empty-floor PNG declaration-lifetime gap. It does not
establish independent remote-byte equality, wire-level request counts, legacy
historical path migration, broader formats/size/uncertainty coverage, populated or
physical AP behavior, public site deletion, custody handoff or full-model parity.


## Installed PNG, JPEG and PDF qualification

The [format lifecycle run](floor-image-formats-native.log) passed in **282.46 s**
(Go **284.845 s**) through the same installed worker image, source loader,
transport, task journal and ownership path. The source snapshot changes from
PNG to JPEG to a valid single-page vector PDF. An identical PDF under a different
full path then receives a distinct path-bound upload completion. Two consecutive
omission/reintroduction cycles of that final PDF each produce a fresh operation
while preserving configuration generation and the same single receipt slot.
There are six source-bound synchronous completion checkpoints in this run; this
is not a wire-level count of HTTP requests.

Original geometry-task recovery, actual Pod replacement, Retain intent retirement
and exact supervised cleanup all pass. The complete typed site baseline is
restored; the connector is Ready with five retained rows, one image slot, and no
active intent or operation. The [source/image/runtime audit](floor-image-formats-native-validation.json)
records the exact fixture paths and final source binding. [Default-off compilation](floor-image-formats-disabled.log)
is recorded separately from the native result. No production provider or
foundation change was needed for these additional formats.

This extends installed qualification beyond the earlier PNG-only boundary. It
covers small deterministic assets on an empty generated floor. Large inputs,
malformed assets, encrypted/multipage PDF behavior, uncertain responses, legacy
historical path migration, populated/physical AP behavior, public Delete and
custody handoff remain unqualified. Completion is a native write acknowledgement,
not remote-byte equality or full resource/model parity.
