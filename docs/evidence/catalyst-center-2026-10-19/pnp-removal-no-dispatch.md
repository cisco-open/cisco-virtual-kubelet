# PnP removal pre-dispatch refusal and fresh replanning

Synchronous PnP removal now uses the existing guard-bound pre-submit refusal
capability. Both the native primitive and its journal adapter preserve the
original guard proof through their shared one-use wrappers. Only errors observed
before the HTTP submit closure is entered can acquire it.

The shared journal may retire the exact Submitting record when that proof is
claimed once, the owner/controller/operation remain unchanged and no completion
receipt exists. It retains creator/baseline history and requires a fresh plan.
Changed ownership prevents retirement. Native/acknowledgement errors after submit
entry still become uncertain and cannot acquire this proof or trigger replay.
No separate lock, retry loop, transport or status schema is introduced.

## Qualification

Controlled and real-API tests pass read/content/source preflight refusal,
zero-Delete retirement, fresh planning and one successful Delete. Changed-owner
refusal retains the journal. Existing lost-response tests still fence Unknown,
including public recovery when the native record is already absent.

The controlled/real-API matrix passes 11 test/package events. Selected
PnP, shared-journal and upgrade/recovery regressions pass 1020 events,
and the five foundation packages/main binary build pass. The native qualifier
passes with exact mutation counts and restored baselines; see
[results and hashes](pnp-removal-no-dispatch.json). It creates a generated,
nonexistent-serial record, captures its ownership baseline, injects a read refusal
before Delete HTTP entry, verifies retirement with zero Deletes and then uses a
fresh plan for the single exact cleanup. It restores PnP/network baselines.
Kubernetes persistence is simulated in the native qualifier; real API tests are
separate.

## Remaining public gates

This is necessary retry/recovery behavior, not new public pruning permission.
New public dispatch still needs whole-cohort native preflight/order and existing
manager-owned bootstrap/physical reservation checks with appropriate actor access.
Consumer census beyond the protected fields, missing-source/CR deletion context,
manager finalization and installed actors remain pending. The full pinned
93-resource NetAsCode model remains incomplete.
