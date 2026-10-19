# Discovery config-cleanup ownership prerequisite

A new private read-only prerequisite observes cleanup candidates for a terminating
pure-discovery config. It reuses the existing owned discovery prune candidate
reader, native footprint validation, config contract checks, section ownership
matcher and metadata digest helper. No transport, deletion writer, receipt store
or manager lifecycle is duplicated.

The snapshot binds config UID/generation/resource version, spec and deletion
request digests, controller UID/generation/resource version, current source
UID/version and the complete retained journal evidence digest. Two full native
observations must produce the same candidates, with source/config/controller
checks repeated around them. Runtime activity is not ownership: active jobs can
be observed without interpreting their status as a grant.

Outstanding journal work, own credential custody, own group cancellation or other
owned sections, malformed/future receipts, missing ownership, foreign UUID aliases
and malformed foreign discovery ownership evidence are fenced. Source/spec/owner
changes and native edits between reads also refuse observation. Existing owned
Delete tombstones require native absence; missing native state with only a Create
receipt is not adopted as successful deletion.

**Public discovery config deletion remains disabled.** This snapshot is not a
submission permit, cleanup completion proof or finalizer release. The existing
feature-only deletion hooks and advertised capabilities are unchanged. The
prerequisite currently has no public runtime caller.

| Gate | Result / scope |
| --- | --- |
| Race regression | 47 families / 391 subcases; 19.799s; discovery/proof/receipt paths |
| Real temporary Kubernetes API | Eighteen cases; 9.140s; actual terminating objects, source/owner/deletion identity races; native service simulated |
| Live opt-out | Current helpers compile and stay disabled without explicit flags |
| Native retained fixture | Read-only candidate verified; retained exact cleanup passed in 5.446s |
| ND | 4.747s |
| Software-upgrade provider | 14.324s |
| Controller GNOI/SWIM | 6.232s |
| Production build | Passed; not installed; observer is not publicly wired |

Native setup used the existing qualified ordinary discovery flow to create one
generated job and establish owned task/UUID/no-op proof. The first harness then
refused an unrefreshed simulated deletion object before cleanup. It retained
UUID `153` and submitted no deletion. The harness was corrected to reload the
stored object representation from its separate simulated API; production
snapshot checks were not relaxed.

The retained job was reverified through its original task, UUID, declared effect
and native baseline. The corrected prerequisite selected exactly UUID `153`,
changed neither the simulated journal nor native mutation counters, and wrote a
private observation checkpoint. The existing retained-fixture cleanup then made
one exact Delete, verified its task/absence and restored the original discovery
baseline. No duplicate job was created during recovery. No installed Kubernetes
object or finalizer was modified. This is simulated deletion/Kubernetes state
with real native reads and separately supervised cleanup, not automatic CR
cleanup or an installed actor test.

Remaining execution work includes manager enrollment, immutable deletion-bound
operation preparation/dispatch, retained-task draining, original-task/absence
receipt publication and manager-only finalizer release. Source loss,
empty/unowned enrollment, broader mixed models and installed qualification also
remain pending. Full pinned-model parity is not claimed.

See [source/snapshot/recovery record](discovery-cleanup-observation-validation.json),
[regression](discovery-cleanup-observation-regression.log),
[API](discovery-cleanup-observation-api.log),
[initial native refusal](discovery-cleanup-observation-native-initial.log),
[retained native verification/cleanup](discovery-cleanup-observation-native-retained.log),
[opt-out compile](discovery-cleanup-observation-optout.log),
[ND](discovery-cleanup-observation-nd.log),
[provider upgrades](discovery-cleanup-observation-provider-upgrades.log),
[controller upgrades](discovery-cleanup-observation-controller-upgrades.log) and
[build](discovery-cleanup-observation-build.log).
