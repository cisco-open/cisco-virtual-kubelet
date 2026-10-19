# Original bulk anycast task recovery

Pinned NetAsCode **0.5.0**, revision
`a435555fe828f03cf3311d52fe1ab6572a247916`, branch
`pr/johalley/catc-support`. Full resource and model parity remain incomplete.

## Execution change

Acknowledged anycast Create operations now reuse the public configuration
journal's existing Unknown-to-Verifying transition through a resource-specific,
read-only observer. A shared structural visitor gives submission and recovery the
same compiled gateway namespace. Other fabric/IPAM callbacks retain their
existing routing. No new transport, submission permit or common lifecycle state
is introduced.

Both static v2 chunks and historical large v1 batches retain their original
member count, selected mask and native dependency/body/existing-UUID digest.
The v2 receipt also retains the original source offset and full source-cohort
digest. A single strict decoder serves selection and verification. Before reading
the original task tree, recovery validates the retained source/operation shape,
parent and original native binding. Missing original members can wait; they do
not authorize a smaller replacement request. Final independent readback still
runs through the existing executor before journal release.

Complete rooted task-tree success is required for both ordinary Accepted and
supported Unknown bulk operations. A failed or pending child cannot be hidden by
a successful root or matching native rows. Missing, malformed or unavailable
original task evidence stays fenced. Removing the bulk opt-in after submission
does not remove authority to observe that original operation; it grants no new
bulk submission authority.

## Verification and corrections

Public recovery tests exercise 45 declarations in original static 20/20/5 chunks,
partial readback without resubmission, option removal, legacy acknowledged large
batches, source/parent/binding changes, replacement of an originally preexisting
UUID, missing tasks, child failures, pending children and unavailable task trees.
The same lifecycle runs against a temporary real Kubernetes API server, including
JSON persistence/restarts. Separate checks cover ordinary Accepted child failure
and pending states, selector refusal and preservation of unrelated callbacks.

An initial real-API fixture discarded the manifest's opt-in annotation when
replacing ObjectMeta. It therefore tested singular operations instead of bulk.
The fixture now explicitly persists the annotation and asserts an original bulk
receipt before injecting uncertainty. Existing gateway/IPAM task fixtures now
serve the complete task-tree envelope. Production recovery has no root-only
fallback for recognized bulk operations.

The final focused race gate passes 264 test events. All fourteen temporary-API
cases pass. The broader regression passes 19,705 test events across sixteen
packages, including ND, shared configuration, maintenance, direct-device and SWIM
paths. The production worker build and strict documentation check pass.

Final gate results, log hashes and source fingerprints are recorded in
[the machine-readable evidence](anycast-bulk-task-recovery.json).

## Native and remaining scope

The authenticated read-only lab prerequisite check again found **zero fabric
sites**. Its accepted native execution gate explicitly skips: this is not a
successful live batch-size, recovery or installed-worker qualification. No fabric,
gateway or device mutation was performed by that check.

An isolated native fabric and its pool/VN prerequisites are still needed for
accepted live request-size and partial/recovery tests. Installed mutation actors,
broader anchored/zone transitions, creator ownership, adoption, prune and
update/delete also remain pending. This change grants no per-object creator or
destruction authority and does not mark the resource or model complete.
