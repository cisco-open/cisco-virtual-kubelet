# Prepared metadata cancellation

Ready, unclaimed PnP cleanup can now be cancelled through the existing manager,
controller journal and bootstrap fence lifecycle. The manager selects cancellation
when the controller is paused or advances generation, the original config is
removed/replaced/edited, or its ConfigMap source disappears or changes incarnation
or resourceVersion. Denied/transient source reads hold custody; they are not
interpreted as deletion. Unchanged intent is not cancelled.

The optional pure product cancellation projection validates the original creator,
owner and complete serial cohort. The shared cancellation helper corroborates the
sealed ledger against every original Lease and the installed recovery authority.
It then competes with the original worker claim on the same controller status
resourceVersion: clearing the exact Prepared operation and writing cancelledAt
happen in one CAS. A winning worker claim leaves Submitting custody held; a losing
or uncertain manager write never becomes permission to clear or replay it.

cancelledAt is distinct from the worker's certified pre-HTTP refusal and from a
successful native outcome. Cancellation produces no absence receipt, native task,
Delete or RefusedSubmissionAt. It preserves every creator row and all original
fences. Subsequent manager reconciles use the existing terminal settlement intent,
exact UID/resourceVersion release and retirement. Lost cancellation acknowledgements
are recovered by reading the original durable marker, without reissuing cancellation.

New claims and acquisition reject cancelled custody. The public PnP path waits
for manager retirement before preparing again. If edited current intent still
requests cleanup, a fresh source-bound operation and complete new fence cohort
are required. Cancellation of stale intent does not mark current intent InSync.
A paused controller does not gain a new dispatch opportunity.

## Schema and deployment

The optional timestamp has immutable, ready-custody and mutually exclusive refusal
validation. Both CRD copies match controller-gen v0.19.0 output exactly; deepcopy
and legacy empty encoding are tested. Native CEL admission confines the atomic
Prepared cancellation to the installed manager. Workers cannot manufacture it,
manager marker-only writes are denied, and the marker cannot be erased before
normal released-custody retirement. The exact compiled metadata policy fingerprint
was updated; matching CRDs, chart and binary are required. Old or incomplete
contracts remain closed. No physical reservation protocol or new transport,
owner journal or controller factory was added; ND has no cancellation hook.

## Verification boundaries

Controlled full cleanup fixtures cover valid, paused, missing-source and missing-worker
cancellation, lost acknowledgements, rejected CAS, a competing actual worker journal
claim, Unknown state, missing runtime/projection, foreign history and replacement
fences. Cancellation must retain original Leases until separate settlement and
leave the creator ledger unchanged. Manager-route tests cover source edits/removal,
pause, disabled authority and unchanged intent. Independent source-invalidation
tests distinguish replaced/versioned/missing ConfigMaps and configs from denied reads.

Kubernetes 1.35 API-server tests use real CRDs, actual actor-separated CEL policy,
resourceVersions and worker claim/cancellation conflicts. They also test worker
forgery, non-atomic manager markers and marker erasure. Kubernetes may classify
native policy denials as Invalid or Forbidden; assertions require the exact
metadata policy to reject the request. Readiness and runtime compatibility facts
are staged, so these tests do not establish deployed-process qualification.

The Catalyst Center lab run creates two fresh generated unclaimed PnP records.
It prepares/seals the first cleanup, edits config generation, cancels the original
operation and settles/retire its custody with zero appliance requests and zero
Deletes. A fresh edited-intent journal is then admitted and both generated UUIDs
are removed exactly once through the normal public cleanup path. Original creator
and absence receipts are retained, and independent PnP and managed-inventory
baselines are restored. Kubernetes storage/authority in this native harness are
simulated; real API enforcement is qualified separately. No switch is claimed,
provisioned or upgraded by this test.

[Machine-readable evidence](pnp-prepared-cancellation.json) records final gates,
source/chart/schema fingerprints, generated UUIDs and the private completed recovery
folder. Those UUIDs were deleted; do not replay the completed qualification.

## Remaining work

This slice requires sealed Ready custody with the full original fence cohort.
Unsealed Prepared slots, unready/partial acquisition retirement, uncertain native
outcomes, in-flight generation changes, legacy settlement migration after a spec
edit, deletion-policy cleanup and full physical claim/provisioning convergence
remain incomplete. Docker's API still times out, so installed manager/worker
qualification remains pending; Docker and existing clusters were not restarted.
Full pinned 93-resource execution/hardware parity and merge readiness are not
established by this change.
