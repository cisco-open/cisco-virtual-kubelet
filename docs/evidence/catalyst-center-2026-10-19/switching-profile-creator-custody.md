# Public switching profile creator custody

Pinned NetAsCode **0.5.0**, revision
`a435555fe828f03cf3311d52fe1ab6572a247916`, branch
`pr/johalley/catc-support`. Full resource and model parity remain incomplete.

## Execution and publication

New public switching Creates use a versioned resource key containing a literal
`#`, which the existing profile-name parser forbids. Legacy literal names,
including slash/colon names, cannot collide with that identity. A legacy
in-flight Create retains its original unowned Retain completion contract.
Current no-op imports and later unowned Updates mint no creator receipt.

The existing executor checks the complete selected native profile set, retained
creator UUIDs and all missing Creates against the shared 256-row action ledger
before returning the first mutation. Source/controller/owner, original journal
and capacity are rechecked through the shared owned-dispatch admission between
native observations. Refusals use the existing one-use pre-submit proof; dispatched
uncertainty and older taskless outcomes retain their fences.

Create publication joins the original strict task-result UUID, complete task tree,
exact declared native name/namespace, source effect and original template/project
binding with stable private native readback. Existing owned metadata observation
checks original source, owner and unchanged journal before and after those reads.
The existing atomic receipt writer persists Verified and one creator result row
together, verifies API survival and refuses replacing another creator operation.
The receipt stores only metadata, the original operation/task/source and UUID;
raw task progress and inherited values stay outside status and hashes.

Generic verification cannot bypass creator publication. Matching creator proof
is mandatory before release of a Verified creator journal. Missing/corrupt/pruned
publication remains fenced; conflicts retry observation/publication rather than
the native call, and a lost publication response resumes from retained evidence.

Retained receipt validation checks section owner name/UID, operation/task/UUID
uniqueness and result-key integrity. Later selected native loss, replacement or reuse under another logical name is
refused before planning another Create/Update. Original creator rows remain
immutable across same-UUID Updates and Retain omission/reselection. This protects
creator identity; it is not a complete latest-effect native baseline or permission
to delete an object.

## Qualification

Public cases cover two independent Creates, full/cohort/last-slot capacity,
missing/corrupt/duplicate/foreign receipts while completion is pending, replaced, renamed
or missing owned native UUIDs, source selection, owned Updates and imports,
wrong task results/services, taskless uncertainty, changed source/owner before
publication, conflicts, lost publication responses and schema-style pruning.
A separate direct gate proves generic verification and receipt-less Verified
release cannot clear the journal.

Real temporary Kubernetes API persistence passes all 62 custody and existing
switching recovery cases. Its map-list schema rejects an exact duplicate ledger
key and preserves the original state; fake-storage cases separately verify
runtime duplicate refusal. An initial fixture expected a duplicate row to persist
and was corrected to require schema rejection. No API or runtime check was relaxed.

The native public cycle creates an isolated profile with a real generated template/
project dependency, performs automatic no-dispatch retirement/replanning and
original Unknown-task recovery, and atomically retains its task/UUID/source
creator receipt. That exact receipt remains unchanged through empty-area
assignment and template membership removal/restoration. Restarted no-ops pass.
Generated profile/assignment/area, template and project cleanup completes. No real
device or template deployment is targeted. Native Kubernetes persistence is
simulated; real temporary-API persistence is a separate gate.

The final race gate passes 794 test events. The broad regression passes
19,870 test events across sixteen packages, including ND, shared configuration,
maintenance, direct-device and SWIM paths. The production worker and strict
documentation builds pass. These results use the final owned UUID/name-alias guard.

Final results, source fingerprints, original native task IDs and log hashes are in
[the machine-readable evidence](switching-profile-creator-custody.json).

## Remaining scope

Latest-effect private baselines, adoption, prune, source/config/controller deletion,
recreation after owned native loss, owner/key/receipt handoff and lifetime capacity
management remain pending. Retained custody is not reconstructed after wholesale
ledger erasure once its journal is gone; no-op imports remain unowned. Installed
actors/schema rollout/downgrade and broader onboarding/composite/multi-site and
physical qualification remain open. Other pinned-resource execution, schema,
default and catalogue gaps are unchanged. No whole-resource, section or full-model
capability flag is promoted by this change.
