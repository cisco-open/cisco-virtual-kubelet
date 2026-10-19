# Public acknowledged bulk task recovery

This extends [owned bulk receipts](wired-bulk-owned-receipts.md) with public
observation of a retained acknowledged task. New Prepared bulk dispatch remains
gated; no new submission capability is enabled by this recovery route.

## Reconstruction and transition

Recovery reconstructs the original batch from complete parsed source declarations,
retained native/physical targets, the logical site UUID and original resource
mask/count. It does not select the remaining unprovisioned subset or let changed
grouping options retarget a recorded task. Every selected member must resolve
once, match its canonical physical serial and requested PROVISION/REPROVISION
state, and belong to the retained grouping hierarchy.

The public acknowledged-group router now recognizes the reserved inventory bulk
prefix. Existing full-member reservation, held claim/live lease, worker/connector,
source, owner and actor checks precede native recovery. A complete rooted task
tree and independent native member/site records, plus repeated managed bindings,
allow only Accepted or acknowledged Unknown to Verifying through the existing
shared BeginGroupVerification CAS. No native provisioning request, claim rewrite,
final receipt, completion, lease release or journal clear occurs.

Verifying explicitly reports GroupConvergenceVerificationUnavailable while
keeping the journal and reservations retained. The real rendered-device proof
provider remains unavailable, so native record presence cannot become final
Verified/settlement. Failed/running tasks, missing records, replaced/pruned group
identity, revoked members and changed source retain the original fence.

## Verification

Native HTTPS and simulated Kubernetes fixtures exercise complete Accepted and
Unknown recovery, running/failed children, missing native members, changed source
and ownership. Full public Reconcile tests additionally use existing shared
lease/claim/worker deployment-chain fixtures, check explicit waiting status after
three simulated restarts, and prove no mutation, action receipt, member completion
or release. Public cases cover accepted, unknown, running, failed, partial,
revoked, changed source and pruned group. Public-only race test passed 3.729s.

Affected wired, fabric public recovery, shared verification/receipt/identity,
journal, precondition and template checks passed: Catalyst Center 35.627s;
shared configuration 6.886s. See [public tests](wired-bulk-public-recovery.log)
and [affected regression](wired-bulk-recovery-regression.log). These fixtures
qualify contracts and routing, not deployed actor/device behavior.

## Live negative qualification

Actual Catalyst Center inventory/assignment/provisioned-record APIs supported
owned planning for all three lab identities with simulated Kubernetes objects.
Missing Submitting journal and missing convergence proof remained refused.
Deliberately injected pruned/untrusted bulk status used an explicitly untrusted
placeholder task string, not a valid native acknowledgement: public recovery
retained Unknown before task observation. No valid task, claim or completion
was invented. Final native identity/provisioned-record reads were unchanged.
Test 11.03s; package 12.093s. See
[live refusal](wired-bulk-recovery-live-refusal.log) and
[sanitized access](wired-bulk-recovery-live-access.log).

No native provisioning POST/PUT or accepted live bulk recovery occurred. No
fabric sites are present in the lab. No capability flag was marked complete.

## Remaining scope

Public options/import/ownership and full-model composition, real all-member
preparation/dispatch, real convergence-proof implementation, final receipt/
completion/settlement/retirement in the public bulk path, installed actor and
API-server qualification, partial-create accepted live recovery, mixed/embedded/
wireless/deletion behavior and a complete accepted live batch remain pending.
