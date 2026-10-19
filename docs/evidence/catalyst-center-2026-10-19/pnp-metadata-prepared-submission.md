# Typed original PnP metadata submission

The private worker path now reconstructs the exact original Prepared removal
plan from retained creator evidence, the complete omitted cohort and the sealed
handoff. It reuses the existing canonical/native baseline validation, eligibility,
managed-serial scan, receipt capacity checks and no-replay mutation transport.
Later UUIDs, another operation, changed custody and Submitting/Unknown replay
cannot enter this path. It does not prepare a new operation or infer success
from native absence.

An explicit ConfigureMetadataHandoff hook supplies installed authority. The
existing journal start delegates its metadata CAS to ClaimMetadataOnce only for
a typed resume slot with this runtime configured. Ordinary source/owner/identity
and PnP admission checks still precede the claim. The successful process permit
is consumed by the common submission sequence. Its checked original epoch is
then reobserved around the existing native cohort guards before DELETE. Unchecked
or denied permits cannot be reobserved into authority. Completed writes share
the existing synchronous receipt builder and original one-use pre-HTTP refusal
proof rather than introducing another journal or receipt format.

Configured metadata execution cannot use unsealed legacy submission: it leaves
an original Prepared slot for the manager. A tampered unsealed Submitting
slot also cannot bypass the configured runtime's native authority guard. Default/unconfigured legacy behavior
is unchanged. Public new pruning and production composition-root activation stay
disabled. Worker finish and new preparation still refuse retained metadata
custody; this stage does not remove fences or fabricate manager release.

## Qualification

Controlled native HTTP cases cover exact acknowledged deletion and creator-bound
absence tombstone, disabled/unsealed/wrong-worker execution, lost claim response,
fresh unclaimed CAS-conflict recovery, lost native response, late sibling
ineligibility, source change and policy change. Winning execution makes exactly
one DELETE. A lost native response remains Unknown without an acknowledgement
receipt and cannot be promoted to success merely because the record is absent.
A claim acknowledgement error leaves Submitting, no process permit and zero
DELETEs. Original native-guard refusals retain immutable handoff/refusal evidence
and original fence UIDs, clear only the certified original operation and preserve
creator history. Neither completed nor uncertain/refused work can replan/replay
through the private new-plan route while custody is retained.

Three isolated real Kubernetes API workflows qualify the same typed execution:
acknowledged DELETE plus verified tombstone, late native preflight refusal, and
lost native response. CRDs, server-assigned UIDs, manager readiness/reference CAS,
Pod-bound worker claim, synchronous verification/refusal writes and schema
retention are real. The appliance HTTP service is controlled. Manager and worker
impersonation use broad test RBAC to isolate actual actor admission. Worker
readiness, background compiler status and remaining deployed compatibility are
explicitly staged. One initial API fixture omitted required worker.name; it was
corrected to the actual worker status shape without weakening schema/admission.

These are not live Catalyst Center or installed-worker qualifications. The
current lab runtime is unchanged. The final tests and exact source fingerprints
are in [qualification results](pnp-metadata-prepared-submission.json).

Final shared API/manager/ND/upgrade regression gates pass 3382 test/package
events. PnP/SWIM/common-journal regressions pass 1221, foundation real API gates
pass 79, and the three real API typed-submission scenarios pass. Main build,
formatting and frozen-source checks pass.

## Remaining full-flow work

Manager settlement must consume only original acknowledged/independently verified
absence or original certified non-dispatch, reconstruct immutable ownership and
custody, mark exact original fences released and delete them with UID/RV
preconditions. Lost-response/restart handling, manager release acknowledgement
and handoff retirement must preserve uncertainty and never adopt replacements.
Only then can a fresh cohort/operation be prepared for the next UUID. Public
routing, installed compatibility, full native lab cycle, consumer protection,
configuration deletion/missing-source finalization and wider pinned 93-resource
execution/hardware gaps remain open. No automatic fallback to direct-device
upgrades or any ND behavior changes are introduced.
