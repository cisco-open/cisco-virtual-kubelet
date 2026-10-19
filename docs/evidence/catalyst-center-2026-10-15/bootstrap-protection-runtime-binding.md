# Bootstrap reservation protection binding

## Implemented behavior

`ReserveBootstrapFence` now requires explicit installation identities and directly
calls the shared `ReadBootstrapFenceProtection` inspector. A permissive remaining
runtime callback alone cannot bypass missing or changed Lease protection. The
record retains the policy/binding UIDs and generations, shared namespace UID,
compiled policy digest, manager username and installation names alongside its
source and connector incarnation. Repeated inspection around creation and the
runtime callback must reproduce the original protection observation. A valid
replacement policy, binding or namespace cannot inherit retained evidence.

The inspector reads the complete typed policy and binding, checks their
protocol annotations, UID/resourceVersion, nondeleting/unowned metadata, observed
policy generation and warning-free completed type-checking. The binding must
reference that exact policy and enforce only Deny without narrowed selectors,
parameters, exclusions or resource rules. The shared namespace must be active,
nondeleting and have a current UID/resourceVersion. All three objects are re-read
after validation; changed or replaced observations refuse the operation.

The entire typed policy Spec is compared with a compiled SHA-256 digest, including
rules, selectors, match conditions, CEL, messages and other fields. Only the two
whole quoted manager/namespace literals and API-defaulted empty selectors are
normalized. Missing/ambiguous identity literals and pre-inserted normalization
tokens are refused. Other expression or whitespace edits change the digest.
The pinned digest is
`d7239261d66c95e289aa623e5fe0b9ee1516ec7d90f26d2eb9078af2eedc11a4`.
The stored fixture comes from the chart; independent Helm rendering and real
API-server round-trip tests enforce agreement with the binary contract.

The readback path reconstructs this complete binding without requiring the
source, worker or policy to remain live. Incomplete old private bindings are
malformed evidence, never absence or authority. No production bootstrap fences
have been created by these qualifications. Existing managed/device/SWIM lease
names and protocols, ND behavior, CRDs and RBAC remain unchanged.

## Verification and important test limitation

Fixtures cover missing policy/binding/namespace, unobserved/uncompiled/warning
status, changed CEL or conditions, Fail-to-Ignore changes, narrowed rules and
selectors, Audit-only binding, wrong policy/parameters/protocol, deleting objects,
terminating namespace, wrong installation identities and late replacements.
Valid same-Spec policy/binding/namespace replacements cannot adopt an old fence;
late policy replacement after creation leaves the original evidence retained.
Empty API-defaulted selectors remain compatible. Source, worker, concurrency,
lost-create-response and no-takeover tests continue to pass.

The focused bootstrap suite passed in 5.415 seconds before the final late-policy
case; the complete shared suite including that case passed in 34.807 seconds.
Provider reservation tests passed in 3.913 seconds; ND/model tests passed from
cache. CC configuration/inventory/PnP/SWIM regressions passed in 70.090 seconds.

The isolated Kubernetes 1.35 API-server test passed in 4.66 seconds (package 6.400
seconds). It independently renders the chart and checks the API-defaulted typed
Spec against the compiled digest. It then invokes the actual reservation helper,
creates the Lease through manager impersonation and real admission, and verifies
idempotent readback and unchanged retained evidence through the denial matrix.
Policy/binding/namespace and Lease API operations are real; controller/source/
worker state and the remaining runtime compatibility callback are simulated.

**Envtest does not run the background policy type-checking controller.** Initial
strict inspection correctly rejected observedGeneration=0 with absent type-check
status. The test explicitly simulates only that controller's status publication
after proving the refusal. Admission enforcement is still exercised by the real
API server. This does not qualify the background controller or an installed lab
bootstrap runtime; production checks remain strict and require observed,
warning-free compilation.

## Native boundary requalification

The updated installation-bound API passed the existing isolated native Prepared
qualification in 4.89 seconds (package 6.776 seconds). Generated serial
`CVKFEDD20E8` received PnP UUID `6ad128b10bc0186771cab026`, operation
`de215229f918da01d3bddc7dcd835624`. Source-bound reconstruction matched simulated
Kubernetes state. The uninstalled runtime was refused before any Kubernetes write
or native grant; denied claim admission retained the Prepared journal with no
site-claim POST. Exact uncontacted fixture cleanup restored empty PnP inventory
and the network baseline. Private evidence is at
`/private/tmp/cvk-pnp-registration-3892736572/`.

## Remaining requirements

Live policy inspection is implemented and required by the reservation helper,
but the production manager authorizer and lab installation are still unwired.
Remaining runtime compatibility must prove installed actor/RBAC/template binding,
new-device enrollment and rollout exclusion. Public dispatch still needs a
one-use grant, cancellation/settlement/release and correlated native workflow/
template completion. The observed protection object and retained Lease have no
native execution methods and cannot substitute for dispatch authority. Full PnP
and full NetAsCode execution parity remain incomplete.


The next [Device enrollment guard](bootstrap-device-enrollment.md) now holds
CiscoDevice manager reconciliation on retained serial evidence. Full installed
rollout/actor compatibility, existing-worker/aggregator/legacy containment,
dispatch and settlement remain pending.
