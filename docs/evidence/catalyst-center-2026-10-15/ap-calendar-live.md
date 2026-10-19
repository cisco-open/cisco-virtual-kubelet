# Native AP calendar qualification

The Catalyst Center NetAsCode branch `pr/johalley/catc-support` now has live
accepted-task/readback evidence for AP calendar transitions through the existing
compiled wireless configuration executor. No production transport, controller
provider, credential or reservation foundation changed for this qualification.

## Scope and source alignment

The pinned NetAsCode 0.5.0 `cc_wireless.tf` maps AP `calendar_power_profiles`
entries to power-profile name, scheduler type, start/end time, weekdays and monthly
dates. Cisco's [Create AP Profile](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/create-ap-profile/)
and [Update AP Profile](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/update-ap-profile-by-id/)
references expose the corresponding calendar settings; the
[Delete AP Profile](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/delete-ap-profile-by-id/)
API targets an explicit profile ID. Documentation schemas alone were not treated
as accepted execution evidence: this test submitted the compiled model and
verified actual task completion, native metadata and subsequent no-op planning.

The isolated model contained a new power definition and a new AP definition with
public settings, timezone Controller and a calendar reference to that power
profile. Both names used cryptographically random suffixes and were absent from
the baseline. The compiled existing power-before-AP dependency graph selected the
power create first, then the AP create. No raw alternate payload/transport path
was introduced.

## Observed execution

The [live run](ap-calendar-live.log) passed in **19.059s** (test body **17.02s**):

1. Created the isolated power definition and verified its UUID/native result.
2. Created the isolated AP definition with DAILY 22:00–23:00 scheduling.
3. Updated the same AP UUID to WEEKLY, selecting Monday and Friday.
4. Updated the same UUID to MONTHLY, selecting dates 2 and 12.
5. Explicitly cleared the calendar using an empty list.
6. At every stage verified native metadata, no-op planning and fresh-compiler
   restart convergence, with unchanged unrelated public definition baselines.
7. Deleted the exact owned AP UUID, then the exact owned power UUID, observed
   their cleanup tasks and verified restoration of the original public baselines.

The existing native reader normalization handled time/day/date representation and
explicit calendar removal; no production behavior fix was needed for these cases.
The existing AP create/update live test was also strengthened with random names,
create-only admission, captured UUID binding for updates and exact UUID cleanup.

## Isolation and limits

Before every mutation the test re-read AP/power baselines and complete recognized
wireless profile/tag references. Unknown reference metadata, a matching site-tag
AP reference, UUID replacement or unrelated baseline drift would hold the run.
Creation required an absent name and a Create plan, never an Update plan selected
by a name collision. Updates and deletes required the UUID learned after the
successful create/task/readback. Uncertain outcomes retain names/identities for
investigation rather than trigger blind cleanup.

The code submitted only definition create/update/delete operations and task reads.
No profile assignment, WLC provisioning or AP/device operation was submitted.
The test compared public metadata baselines; it did not extract credentials or
claim device authentication, actual scheduled radio/Ethernet behavior, adoption,
pruning or multi-profile overlapping-calendar qualification.

This qualifies the existing compiled executor's native definition lifecycle.
It does not qualify installed Kubernetes journal/worker execution: the
[CVK readiness probe](ap-calendar-cluster.log) still returned EOF. Other AP settings,
associations, device rollout and the remaining pinned resource gaps remain open.

## Verification and example

The [wireless race fixture suite](ap-calendar-regression.log) passed in **7.368s**.
The live-tag suite also compiled successfully. Only live-test code changed, so no
new production behavior or shared-provider mutation path was introduced. Strict
documentation, example YAML and whitespace checks are recorded separately.

The repository's `examples/configs/catalyst-center/wireless-ap-calendar-apply.yaml`
uses the same config owner/name as the existing AP example. It defines the power
prerequisite and a weekly calendar without requesting physical association.
