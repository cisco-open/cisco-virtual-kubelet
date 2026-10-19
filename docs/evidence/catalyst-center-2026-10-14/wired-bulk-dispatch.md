# Wired bulk submission through the shared group permit

The private bulk bridge now uses CVK's existing GroupSubmissionPermit and
GroupDispatchPermit, rather than introducing a second claim or lease protocol.
It supports homogeneous POST provisioning and PUT reprovisioning. It does not
activate public preparation or dispatch while the complete convergence and
settlement route remains unavailable.

The bridge reconstructs the retained source/member footprint using the same
helper as acknowledged recovery. It validates the current source revision,
inventory ownership, exact Prepared journal, grouping root, native resource
mask/digest and complete managed member identity before consuming the permit.
Prepared-to-Submitting occurs only through the shared one-use CAS. The native
guard rechecks the exact Submitting journal, original native/member evidence and
source/ownership before and after shared live claim/lease authorization.

Native acknowledgement is recorded with the shared durable task receipt CAS.
If that fails, a known task is returned with the error, preserving the original
submission fence. Lost native acceptance, revoked claims, stale source, changed
controller evidence or replaced devices never cause an automatic retry. Partial
native changes are accepted-task evidence only; they do not create a verified
receipt or authorize settlement.

## Fixtures

Native HTTPS and simulated Kubernetes lease/claim/worker snapshots exercise
POST creation, PUT reprovisioning, partial creation/reprovisioning, lost native
acknowledgement, pre-dispatch denial, missing permit, revocation before durable
receipt persistence, source and physical replacement, foreign ownership,
pruned group identity and controller changes after permit construction. Late
source/device replacement after the submission CAS retains Submitting without
sending a native mutation. A simulated process restart cannot replay a request.
The supplied snapshots and authorization are fixtures, not installed actors.
Every case checks that all retained member claims remain present and that no
completion, manager settlement, released grant or verified action receipt is
fabricated. Final affected race tests passed: Catalyst Center 10.344s and shared
configuration 8.238s, including neighboring EWLC dispatch contracts. See
[affected regression](wired-bulk-dispatch-regression.log).

## Live qualification

The existing three-device read-only test now also invokes the bridge without a
shared permit and confirms refusal. Native inventory/site/provisioning planning,
missing journal/proof refusal and public rejection of untrusted pruned status
passed with native records unchanged. Kubernetes objects were simulated, and
no live claim, task acknowledgement or native provisioning mutation was invented.

Live test passed in 13.13s (package 13.789s). See
[live refusal](wired-bulk-dispatch-live.log) and
[sanitized access](wired-bulk-dispatch-live-access.log).

## Remaining scope

Public options/import/grouping/chunk routing, real all-member preparation and
installed actor dispatch, source-bound rendered-device convergence proof,
public receipt/completion/settlement/retirement, complete accepted live batches,
mixed wireless/embedded/delete semantics and full model composition remain
pending. This stage does not close the bulk resource or any full-section flag.
