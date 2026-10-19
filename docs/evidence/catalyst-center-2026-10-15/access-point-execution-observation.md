# AP execution observation and catalog scope

## Changes

`observeAccessPointConfigurationExecution` reconstructs a private observation from
fresh declared intent, the exact retained cohort digest, accepted task ID and
submission time. It does not need or call the original submission closure.

It uses the existing full task-tree reader, requires a completed rooted tree
with a nonfuture terminal time, and queries each AP by its exact Ethernet MAC
twice. It repeats native dependency/assignment observations and an explicit
caller-supplied journal/source/owner authorization callback. Its final task-tree
read refuses newly failing children or a changed completion time. Running work
returns pending with no usable observation; stale, missing and denied evidence
returns an error.

Returned evidence contains task execution time and actual keyed AP observations.
It has no configuration-complete or release flag. One test deliberately returns
successful task execution with a mismatching selected LED setting: that mismatch
remains in the observation for a future settings verifier. Unknown native
provisioning statuses remain uninterpreted. No Kubernetes state, receipt, actor
or lease is changed, and no native mutation is submitted.

[The Cisco readback schema](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/get-access-point-configuration/)
documents Ethernet-MAC keyed configuration retrieval, but does not specify the
provisioning-status string vocabulary or members of radio role/band objects.
This change does not invent success values or radio-setting mappings from them.

## Scale and identity preservation

Previously each member of every batch snapshot repeated full inventory, sites
and RF-profile queries. One complete catalog now serves the whole cohort within
a single observation, and assignment catalogs remain separate per floor. Every
revalidation collects a new catalog. Singular AP provisioning-dependency reads
use the same resolver; transport/pagination and identity validation remain shared.

The two-member, single-floor dispatch test checks exactly one query per native
catalog/floor in each of four snapshots. A separate two-floor fixture checks
independent assignment observations and refuses a missing second-floor member.
Preparation neither invokes dispatch authorization nor sends native writes.

## Verification and native boundary

`TestAccessPointConfigurationExecutionObservation` covers fresh reconstruction,
unknown and mismatching actual settings, pending/failed/disconnected/future task
trees, changed terminal time, a late child failure, a late native target change,
initial/late denied authority, wrong digest/UUID/MAC, missing/changed member
readback, copied callback inputs and invalid accepted-task metadata.

See `access-point-observation-affected.log` for AP, extranet and SWIM race tests,
and `access-point-catalog-scopes.log` for the additional floor-scope cases.
These are affected-test runs, not a new full-suite or installed qualification.

On 2026-10-15 the existing local tunnel authenticated successfully. Complete
bounded network inventory contained three devices and zero APs. A single random
absent Ethernet-MAC query returned HTTP 404. No controller/device mutation was
attempted; see `access-point-observation-native.json`.

## Remaining work

This is a private native observation primitive, not durable journal recovery or
public activation. The caller's authorization remains a contract to implement
against the real journal/source/owner. Existing physical admission requires an
XE CiscoDevice and live Node; no AP inventory object was made into a fake Node
or admitted by weakening that check. Physical scope and provisioning-completion
proof, independent comparison of every selected setting, durable acknowledgement
recovery, atomic verified publication/settlement and installed/native AP execution
remain open. The AP resource and overall parity remain incomplete.
