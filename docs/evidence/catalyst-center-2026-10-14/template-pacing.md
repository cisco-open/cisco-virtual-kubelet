# Public composite template dependency settling

Pinned input is NetAsCode Catalyst Center 0.5.0, commit
`a435555fe828f03cf3311d52fe1ab6572a247916`, `cc_templates.tf`
`time_sleep.template_wait`: a 10-second wait after regular templates before
composite definitions. CVK implements this inside the existing configuration
executor and shared journal; it creates no Terraform resource or retry engine.

All regular definitions and commits are processed before composites. Regular
and composite commits now retain verification time in the existing bounded
ActionApplications collection, atomically with Verified. Existing receipt
ownership/replacement and schema-preservation checks apply. Composite writes
use correlated current-source/current-generation regular commit verification
times for the settling floor. Already-existing regular templates have no
fabricated mutation receipts: the exact composite Prepared journal supplies a
persisted observation floor via optional `preparedAt`. Missing or pruned legacy
timing evidence cannot grant submission. The marker is set only for explicit
template settling; existing physical reservation digests/admission are unchanged.

The public reconciler keeps a waiting operation Prepared rather than cancelling
it on every pass or treating waiting as an uncertain submission. Source revision
changes retire the old unsubmitted boundary for fresh planning. If native member
drift instead requires a corrective regular-template action, the unsubmitted
composite boundary is retired so it cannot strand the dependency repair. This
case is tested through public reconciliation without a stale composite write. Once timing and
identity checks pass, submission uses the existing no-replay write-ahead path.
The native closure re-observes member versions and parent/template identities;
late member replacement/version change refuses the write. Timing uses a
one-second margin for whole-second Kubernetes timestamp serialization. No
blocking sleep in the controller or new submission authority is introduced.

Local race fixtures cover recent/boundary/legacy/future timing, current and older
revision receipts, duplicate/foreign ownership, missing committed content,
changed source/native UUID/journal, public restart waiting, complete create and
commit, changed ConfigMap, lost response and late member version change. The
last case retains Unknown conservatively even though its closure refused before
the native request; it never replays. Targeted receipt/journal regression passed
in 3.449s; the final public race cases passed in 3.136s.

The broad regression passed: Catalyst Center 233.482s, Nexus Dashboard 4.615s,
shared coordination 31.386s, controllers 34.080s, CLI 11.168s, API 2.327s. The full follow-up run after commit-receipt changes also passed: Catalyst Center
232.982s, shared coordination 31.537s, API 2.332s. The final dependency-drift
boundary/schema tests passed afterward: Catalyst Center 3.597s, API 1.622s.
See [follow-up regression](template-pacing-final-regression.log) and
[final boundary/schema checks](template-pacing-boundary-final.log). See [broad regression](template-pacing-regression.log),
[targeted checks](template-pacing-targeted.log) and [race cases](template-pacing-races.log).

## Live qualification

`TestLiveCatalystCenterCompositeTemplatePacing` ran against the actual appliance
through the public reconciler, using simulated Kubernetes persistence and worker
restarts. `CATC_TEMPLATE_PACING_EXECUTION=isolated-project` explicitly selects
this mutation qualification. Credentials came from the user-provided local file;
password/token were not recorded.

Isolated project `CVK-Q-Pacing-20261014-172833` and regular/composite templates
were created and committed through five native task receipts. Composite accepted
submission times were checked against the persisted regular-commit verification
floor (at least 11 serialized seconds). The model reached InSync, cleared its
journal, preserved ownership and passed a further no-replay reconciliation.
Identity-checked deletion then removed both templates and the empty project,
and final inventory verified cleanup. Test 52.04s; package 52.857s. See
[live execution and cleanup](template-pacing-live.log) and
[sanitized API access](template-pacing-live-access.log).

This is actual appliance mutation qualification with simulated Kubernetes, not
installed manager/worker or real API-server admission qualification. The lab
still has no fabric sites. No template was deployed to a switch.

## Remaining scope

This closes the public definition-path implementation of `template_wait`, not
all `time_sleep` parity. Managed-AP settling already has a separate bounded
receipt/reservation path; complete composition and lab qualification remain.
`provision_device_wait` is conditional on scoped provisioning in the pinned
module, while current configuration Apply owns global scopes only. Scoped
ownership, wired/wireless-to-deployment pacing composition, installed schema/
actor rollout and broader combinations remain pending. No model-section
capability flag was marked complete.
