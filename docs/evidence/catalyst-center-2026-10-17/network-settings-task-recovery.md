# Network settings: original acknowledged-task recovery

Public Apply now recovers an original acknowledged NTP, DHCP, DNS, timezone,
banner or telemetry Update retained as Unknown. Recovery observes the original
rooted task tree and desired-state readback from the same native site UUID. It
never calls the write planner or submits another native mutation. Taskless
uncertainty still stays fenced. This closes the known-task gap recorded in the
[submission milestone](network-settings-submission.md).

## Compiled scope and common contracts

Recovery selection is built from frozen settings in the actual compiled
executor, including the typed credential/settings composition. A prefix or a
same-looking resource name cannot select an undeclared setting. Only the existing
strict acknowledged metadata Update envelope qualifies: original nonce, owner
and revisions, task and submission time, target UUID, no physical group, serial,
credential/history evidence, cancellation or other unsupported bindings.
Credential mutations, credential assignments, AAA and IPAM operations retain
their existing recovery contracts. Normal Accepted completion also keeps its
existing behavior.

The existing `observeOwnedConfigurationOperation` now admits this narrow
network metadata observation. It checks the complete current source, controller,
section owner and exact journal before and after native observation. If the
source composes secret references, the existing complete projection/source
snapshot checks remain mandatory; this change does not add secret resolution or
credential mutation authority. Typed composition selection has fixture coverage;
live mixed projected-secret recovery is not claimed by this qualification.

Each original task observes only its selected setting. Unrelated pending
settings do not acquire mutation authority or block it. The existing network
submission observer supplies stable target/ancestor identities, direct state and
effective inherited values before and after the original successful root and
complete correlated task tree. Pending, failed, disconnected, foreign or stale
task evidence cannot fall back to a successful root. Stable desired readback is
required before Unknown advances to Verifying. Delayed readback can recover on
a later observation, without replay. Current native ancestors are bracketed;
this does not invent an original persisted ancestor baseline or latest-applied
ownership receipt.

The existing common journal owns Unknown-to-Verifying, normal readback,
verification and slot settlement. No schema, transport, reservation, alternate
state store or mutation/release path is added.

## Verification

- [Race regression](network-settings-task-recovery-regression.log), 56.347 seconds: 132 new
  resource/task cases and 13 metadata-envelope/compiled-selection cases cover
  six resource types, normal recovery, running/failed roots and children,
  foreign/stale/disconnected trees, missing tasks, wrong resources/actions/
  targets, mismatch and eventual native propagation, native/replacement/source/
  owner/journal races and successful/racing ConfigMap sources. All negative
  cases retain uncertainty with exactly the original native write. Prior
  network submission/comparison/no-dispatch, telemetry, SDA and bulk network
  cases are included in the same run.
- [Real private Kubernetes API matrix](network-settings-task-recovery-api.log):
  84 public cases pass in 21.199 seconds using actual controller/config/ConfigMap
  UIDs, generations, revisions and status persistence. The native service is a
  controlled fixture; actor admission and an installed cluster are not claimed.
- [Real Catalyst Center public lifecycle](network-settings-task-recovery-native.log):
  87.458 seconds on generated empty hierarchy
  `Global/CVK-Q-20261017-024913-000000000`. All six original native tasks were
  retained as Unknown before restarted public reconciliation. Every original
  full task tree and stable native result recovered; exactly six setting writes
  reached InSync without resubmission. The same cycle also exercised six
  synthetic read-only preflight failures and original-proof retirement/replan,
  repeated no-op reconciliation and exact generated hierarchy cleanup. Three
  hierarchy Creates and one floor Update were setup actions. Kubernetes storage,
  worker interruption and restart are simulated; native tasks/results are real.
  No existing site or device configuration was targeted.
- [Existing observation consumers](network-settings-task-recovery-observation.log)
  cover fabric-owned, RMA and template deployment contracts; [shared foundations](network-settings-task-recovery-foundations.log)
  cover controller/group, ND, device workers, diagnostics, lifecycle, upgrades and
  GNOI. Unchanged foundation packages may use cached results.
- [Current source and pinned module fingerprints](network-settings-task-recovery-sources.json)
  and [strict documentation build](network-settings-task-recovery-docs.log).

## Remaining scope

Taskless and unsupported operation uncertainty remains fenced. Complete
latest-applied ownership/baselines, destructive reset/prune, broader defaults and
inheritance combinations, external telemetry collectors, installed/projected-
secret worker recovery and rendered-device behavior remain pending. SDA semantic
verification and the rest of the full pinned NetAsCode model remain open.
