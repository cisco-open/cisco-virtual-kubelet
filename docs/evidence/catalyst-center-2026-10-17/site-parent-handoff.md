# Authenticated parent handoff for site recreation

Child recreation can now follow a CVK-managed parent replacement through a
versioned `site-private-recreate:v2:` selector. It binds the exact original child
retirement/cohort and the replacement parent's immutable creator receipt digest.
The existing same-parent v1 selector remains supported. A changed ParentID on a
v1 operation, a v2 operation with no parent change, unknown versions and unowned
replacement parents are refused.

## Authority and execution

The replacement parent must have a verified recreation lineage to the exact old
parent UUID retained by the child's retirement. Parent and child share config
ownership and comparison-key scope, with supported kind relationships. The old
parent retirement follows verified child retirement; replacement parent creation
follows parent retirement; child creation follows verified replacement creation.
Generation and original-task/operation bindings are checked throughout.

Planning identifies the current owned replacement by the declared parent path.
The existing private-baseline reader authenticates its current native state,
including a legitimate latest-applied row, and verifies its exact UUID/ancestry.
The singular writer repeats its existing full native ancestor/payload checks
around the common source/owner/journal guard. Retired child and parent lifecycle
UUIDs cannot reappear elsewhere and acquire new authority. No new transport,
Secret API read, journal or status schema was introduced.

Acknowledged recovery returns an observer only. Original task tree, result UUID
and repeated exact desired state still precede the existing atomic archive/new
creator publication. Whole-history readers validate the parent reference even
when that creator later moves into a complete archive. A missing or corrupted
parent chain keeps the completion fence; no name-based or same-state adoption is
allowed.

## Qualification boundary

Structural tests cover combined parent/child chains, invalid/missing parent
references, v1 reparenting, unsupported versions, borrowed task IDs, chronology,
retired parents, dropped history and a referenced creator's subsequent archival.
Temporary Kubernetes API cases exercise serialization and the common status CAS
with synthetic native proofs. These are not installed ownership adoption.

The [final race regression](site-parent-handoff-regression.log) passes
**65 families / 904 subcases** in **81.969 s**.
The [temporary Kubernetes API gate](site-parent-handoff-api.log) passes
**8 families / 106 scenarios** in **75.116 s**.

The [native area handoff run](site-parent-handoff-native.log) passed in **60.59 s**
(**62.789 s** Go duration). It created parent and child, retired child then parent,
recreated parent then child under the new parent UUID, and retired both again.
There were exactly **four Create POSTs / four Deletes and eight distinct original
tasks**. The replacement parent and child shared the same source/config generation,
matching parent-first execution of one declared model. Four original lifecycle
rows remain archived, four current creator/retirement rows remain, section custody
is retained, no active operation remains and the typed site inventory baseline is
restored. No device assignment, workload or physical operation changed.

The [validation audit](site-parent-handoff-validation.json) records task IDs,
exact source hashes and the private recovery checkpoint. Generated comparison
keys and full checkpoints remain private. The qualifier uses CVK's real resource
functions and authenticated transport with simulated Kubernetes persistence and
restarts. Its bounded generated-identity allowlist forbids out-of-scope mutations;
uncertain requests are never replayed or cleaned up speculatively. The separate
[compile-only gate](site-parent-handoff-compile.log) intentionally skipped native
execution and is not counted as live qualification.

## Remaining integration

Public compiler/policy routing, complete operational/settings consumer ownership,
configuration deletion, building/floor native handoff and installed controller
complete-cycle qualification remain pending. The native area scope does not
qualify arbitrary hardware/credential or nested multi-ancestor replacements.
Full site lifecycle and all 93 pinned resources remain incomplete.


## Subsequent local consumer protection

A separate [local consumer fence](site-local-consumers.md) adds strict local
settings/credential absence and keyed shape-drift checks before retirement.
It does not complete all operational reference or public/installed routing work.
The source hashes and native gates above remain historical and unchanged.
