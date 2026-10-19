# Staged site-retirement task and absence verification

The staged retirement observer reconstructs ownership from the persisted
operation, current source/key projection and retained creator/latest receipts.
It does not require the in-memory pre-Delete candidate or deleted native object.
It performs reads only: it does not submit Delete, finish the journal operation,
publish a retirement receipt or release custody.

## Versioned, recoverable proof

The current selector is `site-retire:v2`. It retains kind, path hash, source mode,
scoped key identity and creator/latest receipt digests. It adds two cross-bound
proofs: the pre-Delete full native state MAC, followed by an ancestry identity
MAC that also authenticates that state MAC and the selector. After the leaf is
absent, the ancestry proof can be recomputed from the surviving parent chain.
Changing either proof independently is refused. No deleted path, native
attributes or credential bytes need to be copied into status.

The earlier [v1 staged binding](site-retirement-binding.md) is a historical source
boundary. It cannot prove ancestry after deletion and is not accepted by the v2
observer. No deployed public retirement flow previously emitted these selectors;
there is no silent v1 reinterpretation, resubmission or custody adoption.
Existing creator/update MAC domains and the shared task/journal structures are
unchanged. Ancestry verification protects UUID/name/path/type/parent continuity;
it does not claim parent setting equality after leaf deletion. Full native
attributes remain fenced before dispatch.

## Completion contract

Accepted, acknowledged Unknown and Verifying observations require the exact
current persisted operation and its original task ID. The observer validates
source, controller, owner, key identity, creator/latest digests and a later
configuration input. It rejects borrowed creator/update tasks and missing or
changed lineage. Complete rooted task-tree success is mandatory, including all
children and a non-future terminal time. There is no successful-root fallback or
taskless recovery.

After task success, two complete site reads must independently show absence of
both the original UUID and hashed path. The same path under a new UUID or the old
UUID under another path is refused. Reappearance or a still-present original
object remains unsettled. The parent chain must match the keyed ancestry proof.
Source, owner, key, operation and retained history are checked around readback;
the same original task tree and terminal boundary are rechecked afterward.
A successful observation still retains the original operation and producer rows.

## Verification and remaining work

The [final local race run](site-retirement-recovery-local-v2.log) passes **6**
families / **195** subcases in **10.704 s**, covering v2 binding, recovery,
ancestry, selector privacy and dependency checks. The
[temporary real Kubernetes API run](site-retirement-recovery-api-v2.log) passes
**33** scenarios across binding and recovery in **19.595 s**. It covers actor
reconstruction, current/latest lineage, pruned receipts, edited proof fields,
replacement/reappearance, failed/pending child tasks, changed completion and late
source/owner/key/history/phase mutations. The
[source and gate audit](site-retirement-recovery-validation.json) fixes the exact
qualified source boundary.

There was no new lab mutation in this stage. The earlier native dependency
qualification remains separate; these tests are not an installed automatic
Delete result. Public compiler/policy and submission integration, complete
operational/settings consumer lifecycle checks, atomic retirement receipts,
bounded recreation, config deletion and installed full-cycle qualification are
still required. Full site lifecycle and 93-resource parity remain incomplete.


The subsequent [atomic publication stage](site-retirement-receipts.md) now
commits retirement evidence with Verified and fences generic settlement without
that evidence. This does not change the read-only observer's contract or promote
this historical source boundary to installed public-retirement qualification.
