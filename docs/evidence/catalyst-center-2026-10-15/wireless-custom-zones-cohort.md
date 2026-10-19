# Custom AP zones and policy-tag cohort creation

Pinned model: Catalyst Center NetAsCode 0.5.0, commit
`a435555fe828f03cf3311d52fe1ab6572a247916`. This increment extends the existing
wireless network-profile compiler and ordinary metadata operation journal. It
does not introduce a physical group reservation, new provider or transport.

## AP-zone request validation

The [first native run](wireless-zones-rejected.log) returned HTTP 406 when two
zones used the same SSID/RF-profile pair. A supervised diagnostic, after checking
profile absence and the exact retained disabled SSID and RF reference, returned
[NCND01310](wireless-zones-validation.log): the combination must be unique across
AP zones. The [disabled SSID cleanup](wireless-zones-rejected-cleanup.log) was
verified; no profile had been created.

The merged payload validator now checks uniqueness of each modeled SSID/RF-name
pair across AP zones, including retained observed collections. Different RF
profiles for the same SSID, or different SSIDs for the same RF profile, remain
valid. Public Apply reports DuplicateAPZoneCombination before a native request
or operation journal. Implicit-versus-explicit RF default equivalence is not
claimed by this name-pair check.

## Native multi-tag behavior and implementation

With distinct existing HIGH/LOW RF references, the
[next run](wireless-policy-sequential-failure.log) accepted sequential
single-item policy-tag creates but retained only the second tag. This was
observed for two disjoint zones at the same empty site. The harness stopped at
failed convergence instead of recreating tags indefinitely.

A supervised [single bulk create](wireless-policy-cohort-probe.log), starting
with an empty policy inventory in the exact retained fixture, preserved both
members. Subsequent [individual same-ID updates](wireless-policy-cohort-update-probe.log)
preserved both tags while extending their site membership. All retained test
objects were then [removed with identity checks](wireless-policy-retained-cleanup.log).
The documented [policy bulk API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/create-multiple-policy-tags-for-a-wireless-profile-in-bulk/)
is used; no alternate controller channel or device action is involved.

The executor now creates all-new multi-tag policies for a profile in one normal
Create operation. policy-tag-cohort:v1 resource identity binds the profile,
member resource identities and immutable complete payload. Submission repeats
source/native dependency observation and consumes the plan once, including
failure outcomes. The existing source/owner/journal authority gates the public
path. Complete member readback is required before verification; partial cohorts
remain fenced and cannot generate another create. Existing tag updates retain
the ordinary UUID-bound individual PUT path.

New single-item requests sharing a site/ancestor scope with another existing
policy tag are refused, even for disjoint zones, because preserving an existing
cohort during addition is not qualified. Mixed existing/missing declarations
are likewise fenced with PolicyTagCohortRequired. This is a remaining model
coverage gap, not automatic adoption, deletion or merge support.

An [integrated attempt](wireless-policy-receipt-failure.log) exposed an overly
strict HTTP-200-only check in the new writer. Native inspection found both
members applied; no POST was repeated, and the inspected fixture was
[cleaned up](wireless-policy-receipt-cleanup.log). The writer now reuses the
existing mutation transport success contract and still requires a valid task
ID. An accepted response without a usable task receipt remains uncertain and
non-replayable; returned URLs are not followed.

## Fixture verification

The [final focused races](wireless-policy-cohort-focused.log) passed in 5.371s.
They include the public shared-journal create/restart/update cycle for a two-tag
cohort, a lost acknowledgement retained as Unknown across reconciler restarts,
partial native results, stale source, foreign native scope, one-use dispatch,
malformed receipts and accepted responses with/without a valid task ID.
No missing receipt is fabricated from matching inventory.

[Complete ND/shared foundation package checks](wireless-policy-cohort-foundations.log)
passed (cached). Final wireless/native/manager results are recorded below. Installed Kubernetes actors remain unqualified while isolated cluster
recovery awaits its separate approval.

## Remaining work

Mixed existing/new cohorts, partial-cohort reconciliation, adoption/removal,
large batches, native ancestor-site behavior, partially overlapping zone
selections, transitive reference replacement scenarios, complete default/model
composition, actual WLC/AP behavior and installed actor qualification remain
open in the resource ledger. The lab tests use disabled SSIDs and empty areas;
they do not provision a WLC/AP, assign a device or perform a physical operation.

## Final integrated results

The [fresh native CVK compiler flow](wireless-policy-cohort-native-final.log)
passed in **115.835s**, test body **113.96s**. A disabled OPEN SSID, profile and
two custom zones used the existing HIGH/LOW RF references. Two empty areas were
created and assigned to the profile. One ordinary site tag and both disjoint
policy tags were created; the policies used one integrated bulk Create.
Individual same-ID updates extended all tags to the second site. Retained
readback, no-op convergence, drift reporting and unchanged-inventory conflict
refusals passed at both stages. All tags, assignments, profile, disabled SSID
and empty areas were removed; captured site/profile/SSID/RF baselines were
restored. No device assignment, provisioning or physical operation was submitted.

The [final wireless/profile race suite](wireless-policy-cohort-wireless-final.log)
passed in **9.748s**. It includes partial public readback retained in Verifying
across restarts, then completed when delayed members appeared without another
POST; lost acknowledgement retained as Unknown; and exactly one request from
concurrent consumers. Existing single-tag manifest behavior also passed.
[Final manager/connector/RBAC checks](wireless-policy-cohort-manager-final.log)
passed in **3.778s**. [Strict documentation build](wireless-policy-cohort-docs.log)
passed in **4.68s**, and whitespace checks passed. This is native compiler plus
simulated Kubernetes persistence qualification, not installed actor rollout.

[Catalyst Center SWIM race fixtures](wireless-policy-cohort-swim.log) passed
in **2.792s**. No live device upgrade was run for this metadata-only increment.
