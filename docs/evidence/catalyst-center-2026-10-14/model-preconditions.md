# Pinned inventory and AP preconditions

NetAsCode Catalyst Center 0.5.0 commit
`a435555fe828f03cf3311d52fe1ab6572a247916` uses validation-only
`terraform_data` resources in `cc_access_points.tf` and
`cc_device_provision.tf`. These represent constraints rather than new native
appliance objects; CVK reuses existing parsers in ordinary Apply preflight.

## Changes

The AP radio parser now enforces the previously missing RF-mode constraints:
explicit channel/channel width requires CUSTOM channel assignment, explicit
power level requires CUSTOM power assignment, and 2.4 GHz must omit channel
width entirely. Existing SERVING-role, location-source, bounds and antenna/cable
constraints remain. The positive mapping fixture now uses CUSTOM for explicit
power, matching the upstream precondition rather than accepting an ignored value.

AP configuration/hostname reference parsing is shared between ordinary model
preflight and the existing AP device parser. Unknown profile references and
hostname management without a serial are rejected for every declared state,
including ASSIGN. Profile-level location, role/RF, assignment mode and band/width
constraints are checked even when no AP references a profile. This does not
activate AP configuration execution, skip physical admission or claim device
convergence.

Wireless inventory preflight reuses the existing placement validator for the
four pinned wireless_validation rules: fabric controllers with managed AP
locations require exactly one controller role; declared controllers require AP
locations; anchors cannot be placed on fabric; nonembedded controllers require
type WirelessController. Only the validation projection uses PROVISION to
check facts independently of declared state; the original model remains
unchanged and is consumed by its normal compiler. Null versus explicitly empty
location lists retain the upstream declaration semantics.

The public Apply path publishes ModelPreconditionFailed before ordinary native
planning. Unparseable source continues through the existing syntax/support
compiler diagnostics. Resource-specific group preparation/recovery runs first
and keeps its existing bounded-operation validation. Applying blanket full
provisioning requirements to an existing-only embedded settings operation was
caught by regression and corrected by respecting that routing boundary. Full
model composition across those group paths remains a parity obligation.

## Verification

Eleven invalid-model cases exercise supported single-section manifests through
both Apply evaluation and the full public reconciler: location conflict,
non-SERVING RF settings, channel mode, power mode, 2.4 width, unknown profile,
rename without serial, invalid wireless role, missing locations, fabric anchor
and missing controller type. They publish failed status with zero native API
calls and no prepared journal. Positive declaration cases cover wired inventory,
standalone/embedded/anchor placement and valid AP references/custom RF settings.
Existing embedded-controller public group cycles remain passing. Targeted public
race suite: 8.583s; boundary suite: 8.457s. See
[public tests](model-preconditions-public.log) and
[group compatibility](model-preconditions-boundary.log).

Native lab regression repeated the isolated public composite template model:
project CVK-Q-Pacing-20261014-174022, five task receipts, InSync across simulated
worker restarts, no-replay and identity-checked cleanup of templates/project.
Test 51.62s; package 52.277s. Kubernetes persistence was simulated. See
[live flow and cleanup](model-preconditions-template-live.log) and
[sanitized access](model-preconditions-live-access.log). No AP radio request or
switch deployment was sent or qualified by this test.

## Remaining scope

This is bounded precondition coverage, not complete terraform_data parity.
Bulk-site provisioning validation still depends on an unimplemented public
bulk_site_provisioning option/execution path. Complete resolved-model validation
and composition with bounded group models, AP execution/physical admission and
actual AP behavior, schema/default combinations and installed actors remain
pending. No model-section support flag was marked complete.

The complete corrected Catalyst Center race suite passed in 235.943s; see
[final regression](model-preconditions-final-regression.log). The initial broad
sweep caught the blanket EWLC routing incompatibility described above; ND
(4.694s), shared configuration (31.844s), controllers (33.831s) and CLI (11.421s)
passed in that sweep. See [initial sweep and compatibility failure](model-preconditions-initial-regression.log).
The corrected EWLC/public precondition boundary and the full Catalyst Center
suite then passed. Strict MkDocs build and git diff --check also passed.
