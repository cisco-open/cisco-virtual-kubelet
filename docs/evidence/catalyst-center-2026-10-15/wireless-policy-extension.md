# Existing policy-cohort expansion: native API constraints

Pinned model remains NetAsCode Catalyst Center 0.5.0. Existing all-new cohort
creation and same-ID updates remain implemented and native-qualified. This
increment investigates the separate mixed existing/new declaration gap; it
does not claim that automatic expansion is supported.

## Qualification

The [isolated extension run](wireless-policy-extension-probe.log) created a
new disabled SSID, a profile with three custom AP zones referencing the existing
RF profiles selected from complete inventory (HIGH and LOW were logged; the third reference is not separately logged in this run), two empty areas, a site tag and a two-policy cohort.
Existing retained readback/no-op and conflict refusal checks passed. It then
submitted one explicit native bulk request containing both existing policy names
and a third disjoint policy name. The API rejected this with HTTP 405. This
matches the already-existing-resource classification in the documented
[bulk policy API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/create-multiple-policy-tags-for-a-wireless-profile-in-bulk/).
The diagnostic did not retry the rejected request or enable public upsert.

A separate supervised [unassigned-definition probe](wireless-policy-unassigned-probe.log)
used that exact retained empty-site fixture. Native inventory still contained
the two expected policy names and memberships. Creating the third policy with
siteIds:[] returned HTTP 400, NCND11150: siteIds cannot be null or empty. Native
policy inventory was compared before/after this rejected request and remained
identical. The diagnostic test's PASS denotes confirmed rejection and inventory
preservation; it is not successful unassigned-definition execution. The planned
follow-on PUT was not executed.

All policy/site tags, profile assignments, profile, disabled SSID and empty areas
were then [removed with exact-identity and empty-site checks](wireless-policy-extension-cleanup.log).
Captured public baselines were restored. No WLC/AP provisioning, device
assignment, software operation or physical device action was submitted.

## Product behavior

The current mixed-cohort guard remains in place. Public PolicyTagCohortRequired
status now explains why an unchanged-prefix bulk request or an unassigned
intermediate definition is insufficient, rather than reporting only an
unqualified merge. The new public manifest fixture checks the confirmed reason,
Failed status, unchanged existing policy identity, zero mutation and no operation
journal. Existing all-new cohort, lost-receipt, partial-result and ordinary
update recovery fixtures remain part of regression coverage.

The opt-in CATC_POLICY_COHORT_EXTENSION_PROBE=isolated-empty-sites branch in the
live custom-zone harness reproduces this qualification using absent random
fixture names and three existing RF references. A rejected/uncertain result
retains exact fixture names for inspection; it must not be treated as a passing
capability gate or an ordinary public execution path. Temporary fixture-specific
unassigned diagnostics were removed from the repository.

## Remaining implementation requirement

Mixed-cohort expansion needs a qualified migration sequence that preserves
existing intent and ownership across failure/restart. Bulk creation with existing
names and creation with empty siteIds are not qualified solutions on this
appliance. CVK must not silently delete/recreate existing members under Retain,
assign policies to an undeclared staging site, retry an uncertain operation, or
use an undocumented endpoint to hide this gap. Ownership-based replacement,
retained pre-image/recovery, explicit migration authority and installed actor
qualification remain open; no foundation or other provider contract was changed.

## Regression results

- [Focused cohort/profile/scope races](wireless-policy-extension-focused.log):
  PASS, 5.780s, including the actionable public mixed-cohort refusal.
- [Wireless/profile and SWIM race regression](wireless-policy-extension-regression.log):
  PASS, 10.674s.
- [Complete ND/shared foundation packages](wireless-policy-extension-foundations.log):
  PASS (cached).
- [Manager/connector and worker RBAC checks](wireless-policy-extension-manager.log):
  PASS, 3.932s.
- [Strict documentation build](wireless-policy-extension-docs.log): PASS, 4.83s;
  whitespace checks passed.

These results preserve the existing qualified flows. They do not close mixed
cohort expansion, ownership-preserving migration or installed actor qualification.
