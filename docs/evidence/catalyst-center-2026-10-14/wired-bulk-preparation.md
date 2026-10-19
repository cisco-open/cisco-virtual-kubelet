# Owned wired bulk preparation and composed private lifecycle

The private sequence bridge now records the first pending batch through the
existing configuration journal and shared group identity. It preflights the
complete supplied declaration sequence first, including batches suppressed by
current owned receipts. Native submission closures are discarded, and no
reservation, claim, grant, completion or native mutation is created by preparation.

A verified but unsettled journal blocks preparation. A settled current revision
does not create another PUT operation. A new declared revision receives a fresh
nonce and complete Prepared group identity; a repeated call cannot replace it.
Invalid or duplicate later batches, stale source, foreign inventory ownership
and device replacement prevent partial preparation. Bulk source selection and
all-member admission are still separate requirements; public automatic
preparation/dispatch is not enabled by this private helper.

## Composed lifecycle fixtures

Native HTTPS and simulated Kubernetes fixtures exercise POST provisioning and
PUT reprovisioning through the actual shared contracts: preparation, one-use
all-member claim/dispatch, native task acknowledgement, task/member readback,
Verifying, mandatory independent proof, atomic receipt and settlement-gated
journal finish. Missing proof blocks final verification. A simulated worker
release and manager marker set is supplied only by the fixture; the production
bridge does not write those markers. Restarting after finish neither prepares
another operation nor resubmits the native request.

These tests cover composition of the private stages, not the public manifest
router, installed actors or real rendered-device proof. The test proof is an
explicit simulated callback and cannot qualify device convergence.
The composed cycle passed in 3.088s; see [cycle tests](wired-bulk-cycle.log).
Final affected race checks passed: Catalyst Center 11.397s and shared
configuration 8.316s; see [affected regression](wired-bulk-preparation-regression.log).
Strict documentation build and `git diff --check` passed.

## Live native preparation

Using the lab credential file and the existing HTTPS tunnel, the test observed
all three real native identities and prepared a complete group in simulated
Kubernetes storage. Missing shared permit and repeated preparation were refused.
No native task, reservation, claim, final receipt or provisioning write was
invented; final native identity/provisioning records were unchanged. A subsequent
explicit corruption of the simulated journal separately verified the existing
public pruned-status fence.

Live test passed in 14.43s (package 15.261s). See
[live evidence](wired-bulk-preparation-live.log) and
[sanitized access](wired-bulk-preparation-live-access.log). No fabric sites or
multicast configurations were present in the inspected lab APIs.

## Remaining scope

Public module-option import, source/site grouping and chunk routing, admission
and installed actors, source-bound rendered-device proof, public completion and
settlement, accepted live mutation/recovery, wireless/embedded/delete semantics
and full NetAsCode model composition remain pending. No full-section capability
or resource-parity claim was enabled.
