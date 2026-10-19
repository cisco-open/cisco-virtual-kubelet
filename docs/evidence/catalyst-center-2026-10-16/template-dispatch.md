# Template submission snapshots and one-use dispatch

The project, regular/composite draft and version executors now protect planned
writes through the existing configuration write path. The ordinary wireless
one-use wrapper delegates to the same private helper; no transport, public
provider interface, CRD or journal schema was added.

Project writes repeat selected project UUID/description observations and compare
private compiled intent. When used by the template executor they additionally
repeat the selected template draft/committed/version observations before releasing
a parent-project write. Draft and commit submissions compare complete selected
template observations, including project identity, unmanaged fields preserved in
updates, committed bodies, latest version UUID/number/comment and composite member
state. Composite settling still uses the existing durable receipt/preparation
floor; native observations are repeated after the settling callback.

Private desired bodies remain in memory for comparison. They do not enter status,
errors, journal identities or new unkeyed hashes. The new comparison does not
change the existing composite member-version marker contract.

Every planned closure is consumed once, including preflight refusal, an invalid
receipt and concurrent calls. Subsequent attempts return a transition refusal;
durable reconciliation must own recovery rather than retrying a retained closure.
These are client-side observation checks, not a server-side atomic compare and
swap or proof of native creator ownership.

## Verification

- [Race tests](template-dispatch-tests.log): 50 cases through project and template
  executors cover project create/update, draft create/update, commits, downstream
  template changes before a parent write, source/native/version changes, failed
  reads, lost receipt, concurrent submissions, composite member metadata and
  composite changes during settling. Unchanged plans dispatch once; all stale
  plans dispatch zero times and cannot be retried through the same closure.
- [Public native flow](template-dispatch-native.log): isolated generated project,
  regular draft/commit, composite draft/commit, durable settling, InSync and
  restarted no-replay checks passed. Kubernetes state in this harness is simulated;
  the Catalyst Center calls are real. Both templates and the project were cleaned.
- [Native stale-plan qualification](template-dispatch-stale-native.log): project
  create/update plus regular draft create/update and two committed versions passed.
  Each of the six actions also planned an independent stale write before the
  successful action, then verified stale refusal after native readback and
  transition refusal on reuse of both closures. Exact generated template and
  project cleanup was verified. No templates were deployed to switches.
- [Template and wireless regression suite](template-dispatch-regression.log) passed
  with race detection, alongside the final 50-case dispatch tests.
- [Foundation regressions](template-dispatch-foundations.log) cover shared
  configuration, Nexus Dashboard and software lifecycle/upgrade packages.
- [Strict documentation build](template-dispatch-docs.log) passed.
- [Resource inventory comparison](template-dispatch-ledger.log) retains all 93
  resource types from the pinned 0.5.0 module with no missing or extra rows.

## Still pending

This closes a submission safety gap for three resource types; it does not complete
their upstream field/schema coverage, task-bound creator ownership, pruning,
lost/taskless
outcome recovery or installed cluster/worker qualification. A refused submission
must not be treated as replay permission. Existing conservative journal behavior
is retained. The public native flow above predates the final additional outer
parent-project selection guard; the subsequent project/draft/commit native test
uses that guard. Full installed cluster recovery remains separately unapproved.

A follow-up adds [guard-bound preflight retirement and fresh replanning](template-no-dispatch.md); it does not retire older uncertain records without their original live proof.
