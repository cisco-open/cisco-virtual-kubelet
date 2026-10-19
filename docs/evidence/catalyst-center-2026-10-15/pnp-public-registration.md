# Public PnP database-registration stage

PnP-only inventory Apply now invokes the qualified registration engine through
the public configuration reconciler. The descriptor advertises the bounded
`pnp-registration-stage` capability, not whole PnP onboarding. No new CRD,
transport, reservation protocol or feature flag was introduced.

## Public behavior

The route recognizes declared PNP state or a retained PnP operation. The strict
parser still consumes the complete fragment: mixed PNP/ASSIGN/PROVISION,
unknown fields, extra sections, projected secrets or unsupported retention
cannot be silently dropped while registering an earlier device. Explicit bulk
selection retains its existing gate before PnP routing. Ordinary inventory,
Report mode and other controller providers retain their existing paths.

Registration reuses complete cohort observations, the one-use database POST,
existing write-ahead journal, native UUID readback and atomic ownership receipt.
The public reconciler publishes `PnPRegistrationProgressing` while advancing
those steps. Lost acknowledgements, changed source, pruned UUIDs or unresolved
ownership remain fenced as `PnPRegistrationRecoveryRequired`. Pruning the source
cannot redirect a retained PnP operation into generic inventory completion.

After every registration is owned and verified, the config remains Waiting with
Ready=False and reason `PnPClaimAdmissionUnavailable`. It never becomes InSync
from registration alone. Desired image/template/RF/stack claim fields remain in
the source but are not executed by this stage. No site claim, reset, image
activation, physical provisioning, device reservation or node mutation occurs.
Unknown future claims cannot be completed by the metadata registration receipt.

The example is
`examples/configs/catalyst-center/pnp-registration-execution.yaml`. It explicitly
selects Apply/Retain and documents the pending claim gate. Replace its serials,
PIDs and site paths before use. Public PnP Report and prune/delete remain gated;
supervised native fixture cleanup does not enable those capabilities.

## Verification

Full Reconcile fixtures cover inline and ConfigMap two-device registration,
JSON journal reload and worker reconstruction, atomic UUID ownership, repeated
no-op and Ready=False claim gating. Negative cases cover mixed states, unknown
later fields, extra sections, Report, unsupported retention/model versions,
existing/managed later devices, lost acknowledgements, pruned source/UUID and
late source changes. No physical reservations are created, and source values do
not escape into status. The actual example is exercised through the same public
reconciler fixture.

The initial public/descriptor suite passed in 3.562 seconds; live-tag compile
and those fixtures passed in 3.636 seconds. Configuration, inventory, PnP and
SWIM regression passed in 68.113 seconds. ND and model regression passed from
cache. The final public/example/descriptor and existing Apply-example checks
passed in 3.953 seconds. Adjacent logs retain the results.

The native public reconciler cycle passed in 3.94 seconds (package 6.055 seconds).
Generated nonexistent serial `CVKBD44AE80` received ID
`6ad116a60bc0186771caae8f`. The test reloaded the JSON journal and rebuilt the
worker between transitions, verified owned registration/no-op and Ready=False,
then removed only the exact unclaimed, not-contacted UUID. PnP inventory returned
to empty, shared network inventory matched its baseline, and further public
reconciles refused to recreate the removed owned record without altering its
retained operation/receipts. No claim/reset/device action was permitted by the
trace. Private recovery evidence remains outside the repository at
`/private/tmp/cvk-pnp-registration-277198648/`.

Kubernetes persistence and worker identity were simulated. This qualifies the
public registration route against native CC, not installed Kubernetes admission
or physical onboarding. The earlier
[registration engine qualification](pnp-registration-cohort-stage.md) records
native cleanup and connectivity findings from preceding cycles.

A subsequent [Prepared claim handoff](pnp-prepared-claim-handoff.md) now binds
native state across private preparation/restart and verifies denied admission
without enabling public claiming.

## Remaining parity

Physical PnP identity admission, stack/group coordination, platform/readiness and
projected credentials, correlated workflow/template completion and post-claim
preview sequencing remain incomplete. Mixed inventory/foundation composition,
public reporting, prune/delete ownership and installed-actor qualification also
remain pending. Neither `catalystcenter_pnp_device_claim_site` nor full NetAsCode
parity is claimed.
