# External review - Cisco Virtual Kubelet IOS-XE config branch

**Branch:** `pr/johalley/ciscoconfig_xe`
**Base reviewed against:** `main`
**Review date:** 2026-04-25
**Reviewer:** Codex

This document captures an external implementation review of the Cisco
Virtual Kubelet IOS-XE configuration work, with particular attention to
the RFC claims in `docs/rfcs`, the actual production code paths, and
operator usage for day-0 and day-2 deployment models.

## Executive summary

The branch has a substantial amount of useful infrastructure: CRDs,
writer registries, RESTCONF/NETCONF/gNMI transport scaffolding,
per-family writers, apply logs, bundle fan-out, Helm packaging, and a
live report-mode smoke test against a Catalyst 9300. Several RBAC issues
from the earlier review were genuinely improved, especially around
per-device service accounts and apply-log access.

However, I would not yet call this branch shippable for day-2
configuration management. Several API/RFC promises are still not wired
into the production execution paths:

- transactional apply and save-startup are inert;
- steady-state drift detection short-circuits before device reads;
- the aggregator can run concurrently with per-device VK reconcilers;
- aggregator Helm RBAC is incomplete;
- bundle selector membership changes do not enqueue reconciles;
- configPrereqs teardown does not revert device state despite comments
  saying it does.

The main risk is not the amount of code. The risk is semantic mismatch:
operators will set fields such as `transactional`, `writeStartup`,
`driftDetectInterval`, `aggregator.enabled`, or `configPrereqs`, and the
system will either not perform the advertised behavior or will require an
unrelated event/manual nudge before it acts.

## Review scope

The review covered:

- Updated RFCs in `docs/rfcs`, especially `implementation-status.md`,
  `iosxe-config-driver-review.md`, and
  `iosxe-config-driver-appraisal.md`.
- Core API types under `api/config/v1alpha1` and `api/v1alpha1`.
- The config resolver, reconciler, engine, writers, leases, and
  transports.
- The default per-device `cisco-vk run` path.
- The optional in-process aggregator path.
- CiscoDevice and IOSXEConfigBundle controllers.
- Helm chart RBAC and rendered chart output.
- Verification commands listed below.

## Findings

### Finding 1 - [P1] transactional/writeStartup are still inert

**Location:** `internal/drivers/iosxe/configdriver/engine/engine.go:298-300`

`ResolvedIntent` carries `spec.transactional` and `spec.writeStartup`,
but the engine never starts a transaction, passes no `TxHandle` into
writers, never commits/discards, and never calls `SaveStartup` after
success. NETCONF therefore writes running directly, and `writeStartup`
remains a no-op despite the API/RFC claiming candidate+commit and
save-config semantics.

**Operational impact:** Operators can believe they are getting
transactional candidate datastore behavior and persistent startup-config
saves, while the device is actually being modified directly in running
config with no save step.

**Recommendation:** Move transaction orchestration into the engine:
start a transport transaction when requested and supported, pass the
handle through writer apply paths, commit on full success, discard on
error, and call `SaveStartup` after verified success when requested and
supported. Tests should assert NETCONF candidate, gNMI batched set, and
RESTCONF unsupported/no-op behavior explicitly.

### Finding 2 - [P1] steady-state drift detection is bypassed

**Location:** `internal/provider/config_reconciler.go:224-231`

Once `ObservedGeneration`, `LastAppliedHash`, and `Phase=InSync` match,
reconcile returns before any `Fetch`/`Diff`. That defeats
RESTCONF/NETCONF polling after the first clean apply, and
`SubscribeNotify` events also re-enter the same short-circuit.
`spec.driftDetectInterval` is not consumed.

**Operational impact:** Out-of-band device changes after a clean
reconcile are not detected in the advertised continuous-reconcile model.
This is the largest day-2 correctness gap.

**Recommendation:** Separate "intent changed" from "device freshness".
Use the hash short-circuit only until the next due drift check, or bypass
it when a subscribe notification fires. Store last device-check time or
freshness status separately from last-applied hash, and honor
`spec.driftDetectInterval` with a sane minimum.

### Finding 3 - [P1] aggregator still coexists with per-device VK pods

**Location:** `internal/controller/ciscodevice_controller.go:174-193`

The manager flag says the aggregator runs instead of one `cisco-vk` pod
per `CiscoDevice`, but the CiscoDevice controller still always creates
the per-device `Deployment`, and `cisco-vk run` always starts its own
config reconciler. With the same `IOSXEConfig` namespace/name identity,
the aggregator and pod reconciler can both think they own the same
family lease and write concurrently.

**Operational impact:** Enabling the aggregator can produce two config
writers for the same device/family. The lease identity does not
distinguish process topology, so the lease does not protect against this
specific duplicate-writer scenario.

**Recommendation:** Make the topology choice exclusive. Either do not
spawn VK pods for config-driver-capable devices when the aggregator is
enabled, or add a pod flag/env that disables the in-pod config
reconciler under aggregator mode. The lease holder identity should also
include the topology/process identity if both paths can ever coexist.

### Finding 4 - [P1] aggregator Helm RBAC is still incomplete

**Location:** `internal/aggregator/aggregator.go:151-167`

The in-process aggregator creates a `FamilyLeaser` and runs
`ConfigReconciler.Run`, but the controller ClusterRole does not grant
`coordination.k8s.io/leases` or the scope CRDs the resolver lists/gets:
`IOSXEConfigDefaults`, `IOSXEDeviceGroupConfig`,
`IOSXEInterfaceGroupConfig`, and `IOSXETemplate`. Enabling
`aggregator.enabled` from the chart will fail on realistic
`IOSXEConfig` inputs.

**Operational impact:** The aggregator may work in narrow tests but fail
under normal scoped config, templates, secret-resolved intent, or family
lease acquisition in a Helm deployment.

**Recommendation:** Either mark aggregator as experimental and off the
supported path, or grant the controller service account the same
config-plane read/update and lease verbs needed by `ConfigReconciler.Run`.
Add a Helm smoke test with `aggregator.enabled=true`, leases enabled, and
at least one defaults/group/template-backed config.

### Finding 5 - [P2] VK RBAC still cannot clear replay annotations

**Location:** `charts/cisco-virtual-kubelet/templates/vk-rbac.yaml:71-81`

`recordResult` patches the `IOSXEConfig` object to remove
`config.cisco.vk/replay-from-log` after a successful replay, but the VK
ClusterRole grants only `get/list/watch` on `iosxeconfigs`. ApplyLog RBAC
was fixed, but replay cleanup still needs `patch` or `update` on
`iosxeconfigs`.

**Operational impact:** Successful replays can keep retrying because the
annotation cannot be cleared.

**Recommendation:** Add `patch` on `config.cisco.vk/iosxeconfigs` to the
VK ClusterRole, ideally scoped to the minimum verb needed by the patch
path. Add a chart-rendered RBAC test for replay annotation cleanup.

### Finding 6 - [P2] production reconciler drops YANG validation/defaulting

**Location:** `internal/provider/config_reconciler_controller.go:107-110`

The controller-runtime `Reconcile` path builds a `Resolver` with only
`Client` and `KeyRules`, unlike the polling/aggregator path. In the
default per-pod production path, `spec.targetYangVersion` is not
validated against the supported set and `status.sourceYangVersion` will
not receive the driver default.

**Operational impact:** The API field exists, but its validation and
status reporting are inconsistent across topologies. The default
production path is weaker than the polling path.

**Recommendation:** Pass `SupportedYANGVersions` and
`DefaultYANGVersion` into the resolver in `ConfigReconciler.Reconcile`,
matching `reconcileAll`.

### Finding 7 - [P2] conflict status checks only the first family

**Location:** `internal/provider/config_reconciler.go:399-405`

`ConflictCheck` returns overlaps keyed by every family, but
`recordResult` probes only `familiesKey(cr)`, which is just
`spec.managedFamilies[0]`. If a CR overlaps on `vlan` as its second
managed family, for example, its `Conflict` condition can incorrectly
report `NoOverlap`.

**Operational impact:** Operators can miss real ownership conflicts on
multi-family CRs, especially during migrations or bundle-driven fan-out.

**Recommendation:** Check every family in `cr.Spec.ManagedFamilies` and
aggregate all overlapping owners into the condition message. Add a test
where the overlap is on the second or later family.

### Finding 8 - [P2] configPrereqs deletion does not revert device state

**Location:** `internal/controller/ciscodevice_controller.go:468-476`

When `configPrereqs` is removed, the controller deletes the owned
`IOSXEConfig` immediately. There is no finalizer, no empty-intent prune
reconcile before deletion, and the owned CR does not set
`pruneOnRelinquish`, so the comments/API promise that deleting the
`CiscoDevice` reverts prereq families is not implemented.

**Operational impact:** Day-0 prerequisite config can be left behind on
the device when a CiscoDevice or its prereqs are removed.

**Recommendation:** Either correct the API/docs to say deletion leaves
device state in place, or implement real cleanup: set
`pruneOnRelinquish`, drive an empty desired intent before deletion, and
use a finalizer/status gate so the controller waits for device cleanup.

### Finding 9 - [P2] bundle selector membership is not watched

**Location:** `internal/controller/iosxeconfigbundle_controller.go:252-261`

The comment says CiscoDevice label events pick up devices joining or
leaving a selector, but `SetupWithManager` only watches
`IOSXEConfigBundle` and owned `IOSXEConfig` children. A new
`CiscoDevice` matching an existing bundle, or a label change that removes
one, will not fan out/prune until some other bundle event requeues it.

**Operational impact:** The bundle CR is not reliable as a day-0 fleet
construct for label-selected devices. GitOps users will expect selector
membership to be live.

**Recommendation:** Watch `CiscoDevice` and map label/namespace events
to all bundles in that namespace whose selector may match. Add envtest or
controller-runtime integration tests for device join, label change, and
device deletion.

### Finding 10 - [P2] bundle schema requires the field the controller promises to fill

**Location:** `config/crd/config.cisco.vk_iosxeconfigbundles.yaml:338-341`

`IOSXEConfigBundle.spec.template` is documented as a template where the
controller fills `deviceRef` per device, but the generated CRD still
requires `template.deviceRef`. A normal selector-based bundle manifest
without a dummy `deviceRef` will fail admission even though that field is
overwritten in `upsertChild`.

**Operational impact:** The bundle API is awkward or broken for the
exact fleet fan-out workflow it is meant to support.

**Recommendation:** Introduce a separate bundle template spec that omits
`deviceRef`, or relax the CRD schema for the bundle template while
preserving `deviceRef` as required on standalone `IOSXEConfig`.

### Finding 11 - [P2] secretRefs changes do not trigger per-pod reconciliation

**Location:** `internal/provider/config_reconciler_controller.go:206-214`

The production controller-runtime path watches ConfigMaps but not
Secrets, even though `spec.secretRefs` are resolved from Secrets.
Rotating a family secret will not enqueue a reconcile in the default
per-pod path, so day-2 secret-driven config changes need an unrelated
`IOSXEConfig`/ConfigMap event or manual nudge.

**Operational impact:** Sensitive config rotation is not reliably
declarative.

**Recommendation:** Watch Secrets and map them to `IOSXEConfig` objects
that reference the same `(namespace, name)`, or maintain an index for
`spec.secretRefs[].name`. Add a test for Secret update -> reconcile
request.

### Finding 12 - [P2] gNMI keyed paths are schema-blind

**Location:** `internal/drivers/iosxe/configdriver/transport/gnmi.go:493-507`

`parseGNMIPath` guesses list keys as `name` for strings and `id` for
numbers. Many writers use other key fields such as `tag`, `prefix`,
`first`, or composite interface type/name shapes, so gNMI Set/Delete
paths will be wrong for a meaningful slice of the advertised family set
unless path conversion uses writer/schema key metadata.

**Operational impact:** The advertised gNMI path can fail for non-trivial
families, especially outside the simple name/id cases.

**Recommendation:** Move gNMI path conversion out of the blind transport
parser and into schema-aware writer/path metadata. Alternatively, have
writers emit transport-native path structures or include key-field names
in `transport.Op`.

### Finding 13 - [P3] registry tests are parallel but mutate global state

**Location:** `internal/drivers/registry_test.go:43-45`

The registry tests call `t.Parallel()` while `resetRegistry` swaps the
package-global registry. `go test -race -count=20 ./internal/drivers`
fails with cross-test contamination and duplicate registrations, so the
race-suite evidence in the RFC is currently flaky even though a single
full rerun passed.

**Operational impact:** This is a test reliability issue, not a direct
runtime bug. It weakens confidence in the "race suite green" claim.

**Recommendation:** Remove `t.Parallel()` from tests that mutate global
registry state, or guard test registry mutation with a package-level test
mutex. Keep the concurrent-access test isolated from the reset-based
unit tests.

## Day-0 operational assessment

The per-pod topology is the strongest current path. A Helm install,
CiscoDevice CR, rendered ConfigMap, and per-device VK deployment are
conceptually aligned with the existing apphosting model. The recently
added per-namespace ServiceAccount and ClusterRoleBinding work is a real
improvement for tenant namespaces.

The day-0 fleet abstractions need tightening before they are safe to
advertise broadly:

- `IOSXEConfigBundle` is promising, but selector membership is not live
  and the CRD requires `template.deviceRef` despite the controller
  promising to fill it.
- `configPrereqs` can create prerequisite config, but teardown/revert is
  not implemented as documented.
- Aggregator mode should be considered experimental until it is made
  mutually exclusive with per-device config reconcilers and its Helm RBAC
  is fixed.

For day-0 usage today, I would recommend the per-pod topology with
explicit `IOSXEConfig` objects, conservative `driftPolicy: report` first,
and avoiding aggregator mode for production.

## Day-2 operational assessment

Day-2 is where the branch needs the most work. The RFCs describe a
continuous reconcile model, but the current hot path short-circuits once
the last-applied hash and generation match. That makes the system cheap
at rest, but it also prevents recurring drift reads. Secret-backed family
config changes are also not watched in the per-pod controller path.

The day-2 operational capabilities that should be reliable before a
production rollout are:

- periodic drift detection after `InSync`;
- subscribe-triggered drift detection that bypasses the hash shortcut;
- transaction/commit/discard behavior for NETCONF;
- startup-config save when requested;
- replay annotation cleanup under Helm RBAC;
- accurate conflict status for multi-family CRs;
- documented cleanup semantics for relinquished/deleted config.

Without these, the system is closer to an event-driven apply engine plus
status writer than a full declarative day-2 config controller.

## What improved since the prior review

Several previous RBAC and packaging issues were materially improved:

- The controller ClusterRole now includes `iosxeconfigbundles` and their
  status subresource.
- The controller now provisions a VK ServiceAccount in device namespaces.
- The per-device namespace binding was upgraded to a ClusterRoleBinding,
  matching the cluster-scope informers used by `cisco-vk run`.
- The VK ClusterRole now includes `ciscodevices` and
  `iosxeconfigapplylogs/status`, fixing important watch/status paths.

The remaining RBAC gaps are narrower and mostly around aggregator mode
and replay annotation cleanup.

## Verification performed

Commands run during review:

```bash
git diff --check
go vet ./...
helm lint charts/cisco-virtual-kubelet
helm template cvk charts/cisco-virtual-kubelet --namespace cvk-system
go test -count=1 ./...
go test -race -count=1 ./...
go test -race -count=20 ./internal/drivers
```

Results:

- `git diff --check`: clean.
- `go vet ./...`: clean.
- `helm lint`: clean, with only the standard "icon is recommended" info.
- `helm template`: rendered successfully.
- `go test -count=1 ./...`: passed when local loopback binding was
  allowed. The sandboxed run failed because `httptest` could not bind
  `::1`, not because of code test failures.
- `go test -race -count=1 ./...`: one run failed in
  `internal/drivers`, then a rerun passed. The failure was traced to
  parallel tests mutating the package-global registry.
- `go test -race -count=20 ./internal/drivers`: failed reproducibly with
  registry cross-test contamination and duplicate registrations.

## Recommended remediation order

1. Fix the P1 semantic gaps: transaction/save-startup, steady-state drift
   detection, and aggregator exclusivity/RBAC.
2. Fix production-path consistency: YANG defaulting/validation,
   replay-annotation RBAC, conflict status across all managed families,
   and Secret watch mapping.
3. Fix day-0 fleet constructs: bundle selector watches and bundle
   template schema.
4. Decide and document actual configPrereqs deletion semantics, then make
   code and API comments match.
5. Rework gNMI path conversion to be schema-aware before calling gNMI
   broadly complete for the full family set.
6. Add envtest coverage for controller watch/RBAC behavior and keep the
   race suite deterministic.

## Bottom line

The branch is ambitious and much of the foundation is valuable, but the
implementation status RFC currently overstates readiness. I would treat
the current branch as a strong prototype or late-stage integration branch
for the per-pod topology, not yet as a production-ready day-2
configuration controller. The fastest path to readiness is to close the
P1 semantic gaps and then add integration tests that exercise the exact
operator workflows the RFCs advertise.
