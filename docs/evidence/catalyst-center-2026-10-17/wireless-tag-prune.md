# Managed-baseline-bound wireless tag pruning

Explicit `network_profiles` Apply/Delete can now remove omitted site/policy tags
whose latest private native baseline still matches. The pinned NetAsCode 0.5.0
module emits no tag resource for an empty `sites` list; this now maps to deleting
the owned resource, not PUTting invalid empty membership. Imported matches and
legacy acknowledgements without a private managed baseline are retained.

## Authority and execution

Use the existing config UID, approved `/network_profiles` comparison-key
projection and `prunePolicy: Delete`. A private Create-task result or verified
private Update baseline supplies the managed UUID/content binding. This is an
explicit managed-baseline policy, not a claim that native task output attests
exclusive creation. No new CRD, journal, key channel, store, controller or native
transport is added.

Planning freezes the complete omitted owned cohort before exposing DELETE.
The native parent/name, tag UUID/name, full known fields, recorded dependency
bindings and keyed baseline must still match. All affected site subtrees must be
unused, with complete device/PnP evidence. Unknown peer metadata, changed scopes,
missing-before-write targets, replaced UUIDs, key changes and insufficient cohort
receipt capacity refuse dispatch. Imported/foreign objects are not acquired by
name matching. Retain leaves them unchanged.

A bounded `tag-proof:v3` resource binds one logical member, original native
parent/tag UUIDs, a keyed parent-name/site-path scope seal, key identity and public
bookkeeping predecessor. Its complete producer set must contain exactly that
member and fit the existing 512-character journal field. The existing one-use
preflight/refusal mechanism repeats complete cohort/native/consumer/history
checks before submission. Acknowledged uncertainty observes only the original
task and full tree; taskless, failed or incomplete results cannot replay DELETE.

Publication requires stable exact absence, no same-name or UUID alias, unchanged
scope bindings, empty affected subtrees and original task/tree success, bracketed
by source/owner/controller/key/CAS checks. The existing atomic writer persists
Verified with a private deletion tombstone in the latest Update slot. A Creator
cohort row and every sibling proof remain intact. Creator-only deletion reserves
all required new latest slots before the first DELETE; existing latest slots can
be replaced at the 256-row limit.

Once deletion is conclusively published, later consumers do not undo that fact
or re-block a no-op for the absent tag. Reappearance, forged/resealed private
MACs and implicit recreation remain fenced. Current parent/site scope bindings
are retained; retirement, key migration and later parent/site deletion handoff
are separate pending lifecycle work.

## Dependency ordering and model

The existing in-memory association projection can now represent a qualified
whole-tag DELETE as well as a nonempty membership reduction. It validates the
complete future candidate cohort; it still cannot verify deletion. Actual tag
absence is required before profile/site unassignment is submitted or verified.
This supports omission, explicit empty tag memberships, and an empty wireless
collection without dropping unmanaged associations or borrowing foreign tags.

See `examples/configs/catalyst-center/wireless-tag-prune-apply.yaml`. It revises the
existing owner, retains the parent profile/SSID definitions and removes only
recorded tags and unused association pairs.

## Qualification

Passing terminal gates are recorded below after checking their case counts.
The native API is real; native-harness actor/storage faults are simulated.
Separate temporary Kubernetes API gates check actual status serialization,
generation handling and atomic writes. Installed actors, device/WLC/AP effects
and full 93-resource parity are not inferred from these tests.

The initial broad compatibility command used Go's default ten-minute timeout and
terminated before completing. Old fixture expectations also treated omission of
an owned tag as unsupported and treated v3 as an unknown future version. Those
were updated to preserve the actual unowned-reference fence and use a genuinely
unknown version. Independent final families use an explicit 25-minute timeout;
only terminal, case-counted passing runs qualify below.

| Passing gate | Qualified cases | Go duration |
| --- | --- | --- |
| [Whole-tag lifecycle](wireless-tag-prune-qualification.log) | 60 local + 60 temporary Kubernetes API | 559.548 s |
| [Association pruning regression](wireless-tag-prune-association.log) | 82 local + 82 temporary API | 106.986 s |
| [Dependency ordering regression](wireless-tag-prune-dependencies.log) | 28 local + 28 temporary API + 9 projection | 249.082 s |
| [Membership and receipt regression](wireless-tag-prune-receipts.log) | 34 + 44 local, and the same 78 temporary API cases | 418.124 s |
| [Envelope and example checks](wireless-tag-prune-protocol.log) | v3 single-member producer binding, v2 compatibility, producer sets and example | 2.170 s |
| [SWIM compatibility](wireless-tag-prune-swim.log) | 24 top-level tests | 2.901 s |

The [foundation regression](wireless-tag-prune-foundation.log) also passed;
cached package results are identified in the log. Supplemental
[model](wireless-tag-prune-model.log),
[tombstone](wireless-tag-prune-tombstone.log) and
[foreign-object](wireless-tag-prune-foreign-final.log) gates passed.
[Current source fingerprints](wireless-tag-prune-sources.json) identify the
source and upstream model inputs, rather than attesting an installed binary.

### Native run and retained-fixture recovery

The [initial native run](wireless-tag-prune-native-initial.log) contains a passing
single-policy workflow (622.71 s) and a failed cohort workflow. The cohort reached
the harness's 15-minute workflow deadline after its three tag deletions and first
association deletion were verified. This aggregate log is **not** a passing gate.
The outer Go test deadline was not the cause.

After that process terminated, a bounded read-only task inspection found no
unfinished tasks in the qualification window. Both tag inventories were empty;
one exact generated association remained. The
[inspection projection](wireless-tag-prune-retained-task-inspection.json) records
only bounded diagnostic fields, not raw task progress. It does not attribute an
unacknowledged attempt to an inferred task.

[Supervised retained-fixture cleanup](wireless-tag-prune-retained-cleanup.log)
passed (16.539 s), removed the remaining association and generated parent/disabled
SSID/empty areas, and verified the unrelated baselines. A cohort-only rerun from
that clean state uses a 30-minute workflow deadline. Its outcome will be recorded
separately; the initial failure is retained.

The [30-minute cohort rerun](wireless-tag-prune-native-cohort-final.log) subsequently
failed at 920.05 s on the harness's mutation/recovery/refusal proof assertion,
not its deadline. Three tag deletions and the first association deletion had
already passed task/tree/readback qualification. The final association task was
acknowledged; [post-run inspection](wireless-tag-prune-cohort-final-task-inspection.json)
confirmed successful task completion and empty tag/association inventories.
This readback does not retroactively supply the missing harness proof or make
that test pass. [Exact fixture cleanup](wireless-tag-prune-cohort-retained-cleanup.log)
then passed (13.911 s), restoring all unrelated baselines.

The native harness had attached its guard/observer to one REST session, while
the client's existing 15-minute session lifetime rebuilds that REST client.
Later requests could consequently bypass the harness observer. An optional
internal transport decorator now survives every session build; the serial
profile qualification installs and removes its observer across the entire
operation, including the latest renewed session. Default transport construction,
TLS, credential handling, mutation no-replay behavior and other providers are
unchanged. Failure diagnostics now identify counter/tree/refusal mismatches
without exposing request bodies or credentials.

The [deterministic renewal regression](wireless-tag-prune-session-renewal.log)
passes both forced lifetime renewal and GET-401 renewal, verifies exact observed
mutation counts and continued scope denial, and verifies observer removal from
the latest session. At that point, native cohort qualification was still missing;
the subsequent passing result is recorded below.

[Default client/SWIM regression](wireless-tag-prune-client-renewal-regression.log)
also passes authentication classification/redaction, GET-401 refresh policy,
redirect refusal, mutation no-replay, stale-session invalidation, credential
rotation, TLS defaults and SWIM contracts (2.591 s). The
[original lifecycle-gate source fingerprints](wireless-tag-prune-sources-before-session-renewal.json)
are retained separately from the current source with the observer hook.

The same session-stable helper is now used by six additional existing native
qualification paths: network settings, explicit-empty network settings,
DNS omission/clear/null, partial telemetry, template pacing and site design.
Their duplicated one-session transport setup has been removed. These are harness
corrections, not new provider capabilities or fresh native qualifications of
those resources. The [local renewal gate](metadata-session-callsite-final.log)
compiles those paths and exercises lifetime/GET-401 renewal, retained mutation
denial/counting and observer removal. Its
[source inventory](metadata-session-callsite-sources.json) identifies the exact
shared helper and changed harnesses. The whole-tag native cohort retains
its own separate result and source evidence.

### Passing native qualification

The [session-stable cohort rerun](wireless-tag-prune-native-cohort-renewal.log)
passed in **959.97 s** (962.055 s including Go test overhead). All fourteen original
acknowledged tasks recovered with complete task-tree/native readback proof and
exact mutation counts; fourteen injected no-dispatch refusals were retired.
All three manifest-driven tag deletions completed before either of the two
strict profile/site unassignments. The same source then reached final InSync and
repeated no-op, retaining five private tag rows/six member proofs and two
association tombstones. Generated parent/disabled SSID/empty areas were cleaned
and unrelated inventories restored. No device assignment, provisioning or
physical operation was submitted.

Together with the earlier passing single-policy workflow (622.71 s), the
[independent evidence audit](wireless-tag-prune-native-audit.json) verifies:

- 26 distinct original task IDs across the two passing workflows;
- five manifest-driven whole-tag deletions before four strict association
  deletions, with ordering checked independently for each workflow;
- every native v3 selector's exact single-member producer digest and existing
  512-character bound;
- nine retained private rows/ten member proofs across the two workflows, plus
  both restored baselines and the final no-op assertions in the raw logs.

The failed attempts above are excluded from those passing totals. The final
cohort crossed the client's 15-minute session lifetime with its observer and
guard intact. The first single-policy pass preceded the final single-producer
admission check and observer hook; its recorded selectors independently satisfy
the producer constraint, and current code/API/renewal gates qualify those added
guards. These are real native API cycles with simulated Kubernetes actors;
installed-worker and physical-device qualification remain separate gaps.

The timed-out broad local gate also left two temporary envtest child processes.
Their exact temporary binary/data paths and creation times were checked against
the failed run before SIGTERM. Both exited; other processes, temporary data and
the installed cluster were untouched. The
[cleanup audit](wireless-tag-prune-test-process-cleanup.json) records that bounded
process cleanup.

## Remaining full-goal scope

Transitive dependency representation/qualification, automatic recreation/cohort migration, adoption/handoff, key and tombstone
retirement, larger scopes, config/profile/site destructive lifecycle,
nonempty-device admission, installed workers, physical wireless qualification and
other remaining resource/schema/default/execution gaps remain open. This is
whole-tag pruning for the qualified managed scope, not full model completion.
