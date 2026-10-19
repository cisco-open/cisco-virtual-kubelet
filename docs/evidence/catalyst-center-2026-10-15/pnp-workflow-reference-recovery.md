# PnP workflow reference and work-item recovery

The pinned NetAsCode 0.5.0 PnP claim selects a site, optional software image,
optional onboarding template and, for APs, an RF profile. This addition extends
the existing private owned claim recovery observer; it does not advertise public
PnP Apply or release claim reservations.

## Accepted-operation observation

After reconstructing the retained claim resource binding and repeating the
native dependency and physical inventory observations, recovery now compares the
main workflow's present image/config IDs with the requested resolved identities.
Image comparison applies only when activation was explicitly requested; a skipped
image is not an activation obligation. Template comparison uses the base template
UUID sent by the pinned module, while the existing journal key still binds the
committed version and project independently.

Missing workflow identity or requested references yield explicit pending evidence.
A conflicting present requested reference is rejected even if another reference
is missing. System/reset workflows cannot substitute for the main workflow.
Matching references do not prove template version, variables, task success,
submission correlation or claim completion. Native state names and timing units
remain uninterpreted. The observer never advances an operation, writes an
application receipt, replays a claim or releases a reservation.

## Safe workflow projection

The shared PnP reader now retains nested work-item state and start/end timing.
Repeated recovery can therefore detect a changed work item even if its parent
task remains unchanged. Commands, CLI output, task names, descriptions and
configuration parameters remain discarded. Native timing must be a nonnegative
safe integer; known nonzero intervals cannot be inverted. Bounds are 1,000 items
per task and 10,000 per workflow, alongside the existing 1,000-task bound.
Unknown native states remain metadata, not guessed terminal outcomes.

The documented wire fields are defined in Cisco's
[PnP device-list API](https://developer.cisco.com/docs/catalyst-center/2-3-7-4/device-onboarding-pnp-get-device-list/).
The API schema does not establish correlation or successful completion merely
from matching references. Native workflow completion remains a separate gap.

## Tests and lab limits

HTTP and owned-journal fixtures cover stable matching and conflicting image and
template references, missing references, skipped images, system/reset-only
workflows, changed source/owner/dependencies and nested work-item-only changes.
They assert that both the complete active operation and application receipts
remain unchanged, with no native writes. Projection fixtures check sensitive
field removal, malformed timing, per-task and aggregate bounds, and unknown
states. These tests use simulated Kubernetes persistence. The affected
PnP/inventory-deployment race suite passed in 7.518 seconds; ND and model
regression tests passed from cache. The broader LAN/PnP/inventory
regression suite passed in 240.590 seconds, covering shared-reader callers.

Native repeated read-only inventory finds zero PnP records and three managed
network devices. The final native test passed in 0.52 seconds
(package 2.404 seconds). The accepted-owned-claim subtest is explicitly skipped. No
registration, claim, reset, preview or deletion was submitted. This proves only
the safe empty-inventory read boundary; positive workflow comparison and claim
completion have not been qualified on lab hardware.

Public registration/claim execution, real PnP physical admission, stack/group
coordination, readiness and credential gates, complete workflow recovery,
post-claim preview sequencing and installed/lab execution remain pending. Existing
ND, direct-device upgrades and SWIM behavior were not changed. See the adjacent
logs for fixture, LAN-caller regression, ND/model and native results.
