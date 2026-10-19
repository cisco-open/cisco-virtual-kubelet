# Repeated wireless-tag recreation through retained controller state

The existing v5 implementation now has reconciler-driven qualification of two
complete recreation cycles, including ordinary updates and membership reduction
between them. No production controller, transport, CRD or foundation change was
needed for these added cases.

Four new cases cover singleton and bulk policy producers with both stable and
fresh native identifiers. After the first recreation, each case changes the site
tag AP-profile reference, removes one tag-site membership while retaining both
profile-site associations, deletes every owned tag, and recreates the complete
producer sets again. Each reconciliation constructs a fresh reconciler using
retained storage. Original task recovery, exact mutation counts, same-incarnation
identity/lineage continuity and changed private baselines are checked. The second
recreation retains four complete archived cohorts and reaches InSync with three
additional no-op reconciliations and no request replay.

The mock native service was corrected to preserve existing identifiers on PUT
after recreation and to distinguish successive fresh-ID generations. Stable-ID
cases preserve the same identifier across both generations. These are test
fixtures, not claims about an unobserved appliance response.

## Terminal qualification

- [Local race gate](wireless-tag-repeat-recreation-local.log): **121.465 s**.
- [Temporary Kubernetes API race gate](wireless-tag-repeat-recreation-api.log):
  **308.937 s**.

Each passes **23 execution cases**, including the four repeated-cycle cases,
alongside 84 original-history and 89 archive scenarios and the selected lineage,
readback, protocol and SWIM compatibility checks. The singleton partial-bulk case
is inapplicable and skipped. [Case counts and source fingerprints](wireless-tag-repeat-recreation-validation.json)
identify these test-only additions separately from the previous 19-case gates.

## Native boundary and next qualification

The separately running singleton/bulk lab command compiled before these test-only
additions. Its [stable-identity report](wireless-tag-recreate-stable-identity.md)
qualifies one recreation followed by pruning and cleanup. It does **not** qualify
the new repeated-cycle update/member transitions against the native appliance.

A dedicated, explicitly gated live entry point has been prepared for that next
step. It retains both already-owned empty-area associations, expands each tag's
membership to the second area using ordinary same-ID v5 Updates, reduces it again,
and runs a second delete/create cycle. New tasks, private baselines, unchanged
incarnation lineage during Updates, and four complete predecessor cohorts are
checked before final ordered tag/association pruning and baseline restoration.
Conflicting association-removal qualification is refused before fixture creation.
The ordinary membership expansion avoids introducing an external AP profile or
assigning any device. Compilation and disabled-gate skips are preparation only;
native qualification must run serially after the current fixture is cleaned up.
The [compile log](wireless-tag-repeat-live-compile.log) confirms both default-off
entry points skip. Four [negative gate checks](wireless-tag-repeat-live-refusal-gates.json)
also verify missing recreation consent or conflicting association removal is
refused for both entry points before client/fixture initialization.

Larger/mixed scopes, ownership/key/archive retirement and handoff, parent/config
deletion, association resurrection, installed workers, physical WLC/AP behavior
and remaining 93-resource model gaps are still open.
