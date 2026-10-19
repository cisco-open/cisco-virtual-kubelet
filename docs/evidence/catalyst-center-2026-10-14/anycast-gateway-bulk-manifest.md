# Bulk anycast manifest execution and retained recovery

This extends the [native batch stage](anycast-gateway-bulk.md) into the existing
anycast executor and public Apply path. The opt-in annotation is
`cisco.vk/catalyst-center-bulk-anycast-gateways: "true"`. Absent or false preserves
singular creates. The option remains outside the resolved NetAsCode model and
does not replace the inventory bulk or site bulk options.

## Execution and recovery

The executor groups complete source cohorts by native fabric identity and
preserves existing parent-before-zone ordering. If a member requires an
uncreated parent, its complete cohort waits. Every ready cohort is preflighted
before a write is returned; unsupported later intent cannot hide behind an
earlier write. The full source state is observed around authority as well as
the native batch's own fresh dependency checks.

Public binding walks the existing configuration groups and fabric admission
wrappers, retaining their resource prefixes, including combined IPAM/fabric
prefixes. Submission requires the exact Submitting journal resource, native
parent, create action, current config/controller/source revisions and one
matching fabric section owner. Missing/foreign owner, changed source, wrong
parent, Prepared phase or paused controller refuses the guard. Existing generic
configuration journaling records task acceptance and polls native completion;
no parallel task or ownership store was introduced.

Bulk readback reconstructs the complete original native-fabric source cohort,
not the remaining missing subset, and validates the original mask/digest plus
every native member. Foundation and IPAM prerequisites still precede verification.
Retained bulk verification works after the annotation is removed: new work may
use singular execution, but an accepted batch cannot be abandoned or replayed
because its transport option changed.

This uses the same logical gateway-create policy as the existing singular
executor. It does not widen physical device admission, SWIM, ND or direct upgrade
contracts. Retain remains required for this option; update/delete and adoption
are not silently enabled.

## Fixture qualification

Full public Reconcile tests use native HTTPS and simulated Kubernetes persistence
for opt-in parent/zone creation, combined IPAM creation, secondary-pool support,
lost gateway acknowledgements, replaced parent readback and removing the bulk
option after acceptance. The existing dependency ordering, retained journal,
ownership and restart/no-replay assertions apply. Separate executor fixtures
send multiple gateways in one request and recover partial original cohorts.
Parent-ordering tests cover bulk dependency refusal and replaced parents.

Affected public/executor tests passed in 18.987s. Nine binding cases passed in
2.861s. See [public/executor tests](anycast-gateway-bulk-manifest-tests.log) and
[binding tests](anycast-gateway-bulk-binding-tests.log). These fixtures do not
qualify installed workers, a Kubernetes API server or actual device convergence.
The complete race suites passed for the public-route snapshot: Catalyst Center
258.250s and shared configuration 31.509s; see
[full regression](anycast-gateway-bulk-manifest-full-regression.log). A final
whole-source preflight refinement then rejects a mismatching existing member
even when its cohort waits on another parent. Final affected checks including
that case, all public bulk cases and ownership bindings passed in 19.408s; see
[final checks](anycast-gateway-bulk-manifest-final-checks.log). Strict documentation
build and `git diff --check` passed.

## Lab limitation and remaining scope

The authenticated native prerequisite check is repeated for this stage. The
lab currently has no fabric site, so accepted live gateway execution is skipped;
no fabric/gateway/task or live claim is fabricated. Installed actor/API-server
qualification, isolated accepted live create/readback/cleanup, explicit native
request-size qualification and chunking, broader anchored/zone batch transitions,
ownership/adoption/prune and update/delete semantics remain pending.

The public route is opt-in and fixture-qualified, not full model parity. No
resource or full-section capability flag was marked complete.
The repeated lab check recorded zero fabric sites and explicitly skipped accepted
execution (0.42s; package 1.077s). See
[native prerequisites](anycast-gateway-bulk-manifest-live-prerequisites.log) and
[sanitized access](anycast-gateway-bulk-manifest-live-access.log).
