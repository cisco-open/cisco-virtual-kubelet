# PnP Prepared claim handoff

PnP claim preparation and resumption are now separate private adapter stages,
using the existing configuration journal and admission interfaces. Public
registration remains available; public site claiming is still gated. This does
not introduce a new reservation protocol or claim completion path.

## Architecture finding

The current shared reservation candidate requires a CiscoDevice, Node, ready
network worker and controller worker, and binds their immutable identities plus
network inventory/physical identity. An unprovisioned PnP record does not supply
those objects. Its native PnP ID cannot substitute for the managed-device binding.
A bootstrap identity/admission path, including cancellation and settlement, is
therefore still required before public claims can dispatch. Existing managed
provider admission is unchanged.

## Durable preparation and resume

Preparation verifies retained registration ownership and current source, resolves
native claim dependencies, freezes the request and persists Prepared without a
permit or POST. The new resource key has the form
`pnp-claim:v2:<dependency digest>:<native target digest>`. The dependency digest
retains the existing resolved site/image/template/project/version/RF identities.
The native digest binds the safe pre-claim physical and workflow projection,
including nested work-item state/timing. Credentials, day-zero configuration,
commands and output are discarded before hashing; source values and template
content are not stored in the key. Canonical sorted serials preserve physical
identity across serialization.

Resume reconstructs the plan from the exact owned Prepared operation and requires
the original v2 resource binding. A changed native state/workflow/member, replaced
record, changed source/owner, absent registration receipt or unsupported binding
is refused before requesting admission. Legacy Prepared keys cannot gain missing
baseline evidence by replanning. Submitting, Unknown and Verifying cannot enter
the submission path. A fresh in-process physical permit remains mandatory.

A narrow optional hook in the CC journal helper performs native preflight after
Submitting and before permit consumption. PnP brackets those reads with the
existing source/owner/exact-operation observer. The final permit check therefore
immediately precedes the write-only POST continuation. Existing callers pass nil
and retain their original ordering. Native preflight or source failures retain
Submitting without a POST; denied admission before Submitting retains Prepared.
Neither case automatically replays. A lost POST response remains Unknown.

The native plan retains one-use guards across both preflight and dispatch. The
existing one-shot private claim bridge delegates to the same prepare/resume
implementation rather than maintaining a second execution path.

Acknowledged legacy keys remain readable with their earlier dependency-only
observation semantics. For v2 acknowledged work, recovery compares dependency
bindings and validates the before-digest shape but allows native state to change
after the claim. It does not compare post-claim state to the original pre-claim
hash or treat matching references/inventory as workflow completion.

## Verification

HTTP/journal fixtures serialize Prepared and rebuild the adapter, then exercise
stable resume, native state/workflow/work-item changes, replaced UUIDs, legacy or
future Prepared formats, corrupt bindings, changed source/owner, missing receipt,
wrong operation and missing admission. Late native/source changes, denied permits,
lost acknowledgements and repeated resume prove fencing and no replay. Event
ordering requires the permit check directly before the claim POST, after every
native read. Changes only to discarded output/configuration do not invalidate
the binding. Recovery fixtures cover legacy acknowledged keys, malformed/future
bindings and expected post-claim native state transitions without completion.

The final PnP/live-build suite passed in 11.220 seconds. Shared reservation,
ND and pinned model tests passed from cache. Broader shared journal/configuration,
inventory and SWIM regression passed in 70.488 seconds. Adjacent logs retain the
results.

Native qualification used generated nonexistent serial `CVKD5D14688`, PnP ID
`6ad11a9d0bc0186771caaeea`, and Prepared operation
`b5cd3ded7bc9cd1397818d87180bf930`. Native dependencies and the exact target were
bound, journal JSON was reloaded, the worker was rebuilt, and resumed planning
revalidated the same binding. Deliberately denied admission left the exact
Prepared journal unchanged and issued no site-claim POST. The trace allowed only
registration and exact unclaimed/not-contacted fixture removal. Cleanup restored
empty PnP inventory and the shared network baseline. The test passed in 5.25
seconds (package 7.293 seconds).

Kubernetes persistence and identity were simulated. The Prepared journal remains
only in private fixture evidence, not as a pending operation in the live cluster.
No real device claim, reset, upgrade or provisioning occurred. Private evidence
is at `/private/tmp/cvk-pnp-registration-245782595/` with 700/600 permissions;
credentials and raw native responses are excluded from repository evidence.

## Remaining parity

Bootstrap physical identity/admission, stack/group support, platform/readiness and
credential gates, correlated native workflow/template completion, safe
cancellation/settlement, public claim dispatch and post-claim preview sequencing
remain pending. This qualification proves preparation and refusal of unadmitted
dispatch, not native claim acceptance or full NetAsCode parity.
