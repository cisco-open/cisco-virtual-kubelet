# Shared switching and wireless profile/site recovery

Profile/site associations now use the same metadata dispatch/recovery machinery
as wireless profile and tag definitions. Common profile recovery lives in
`network_profile_recovery.go`; the wireless-specific intent snapshot stays
separate. There is one association executor for both namespaces, one task-tree
protocol, one controller journal and the existing source/owner/controller/CAS
checks. No CRD, transport, credential channel, controller process or physical
mutation-admission structure is added.

## Preserved identity and submission contracts

The existing switching `sites/` and wireless `wireless-sites/` resource spellings
remain unchanged. The logical pair retains its existing name/path digest, with
profile UUID in ParentID and site UUID in TargetID. Names containing slashes are
not incorrectly split into another namespace. Association operations remain
Update actions with a single native POST body containing the selected site ID.

Planning freezes the complete declared selection and repeats its observation
twice before invoking the mutation client. The existing one-use guard now also
issues the shared private refusal proof if that preflight fails. Exact unsent
journal retirement preserves owners and application/credential history, then
fresh planning can proceed. The original guard remains consumed; API mutation
errors or lost acknowledgements cannot issue that proof or authorize replay.
The frozen-caller behavior and additive preservation of unrelated memberships
remain unchanged.

## Acknowledged recovery

A metadata-only Unknown operation with its original task can recover through the
existing common owner/source/controller/journal bracket. Selection requires the
correct namespace and the exact declared logical pair. Observation reconstructs
only that pair, checks the recorded profile/site UUIDs, reads the original task
root and full tree, and requires stable presence observations before and after.
Normal verification still independently repeats its pair readback.

Membership reads for other declared pairs cannot strand the acknowledged pair.
This is a bound on membership observation, not a claim that shared paginated
profile and site inventory APIs query only one object. Pending or missing future
peers still block their own later planning. They cannot trigger another request
for the already acknowledged pair.

Normal Accepted completion retains its distinct Verifying boundary for partial
native readback. A missing acknowledgement remains taskless Unknown even when
the desired association is present. Wrong task roots, failed/pending children,
unavailable trees, removed associations, replaced profile/site UUIDs, mid-tree
identity changes and source/owner/controller changes remain fenced.

Controller generation tests use a supported mutable version-pin change. The
real API server rejected the initial fixture's attempted endpoint change; the
fixture was corrected without weakening immutable endpoint admission.

## Qualification

[Focused race qualification](profile-site-recovery-focused.log) covers existing
complete-selection/frozen-plan/one-use behavior and [34 association lifecycle
cases against a temporary Kubernetes API](profile-site-recovery-api.log), split
between switching and wireless. Cases include automatic refusal retirement,
acknowledged recovery, all uncertainty fences above, ordinary partial readback,
missing future peers and slash-bearing profile names.

The [broader race regression](profile-site-recovery-regression.log) covers profile,
wireless, switching, shared no-dispatch and template task-recovery paths. Shared
[controller, ND, admission, lifecycle and GNOI checks](profile-site-recovery-foundation.log)
remain passing. A [read-only retained assignment-tree probe](profile-site-task-tree-readonly.json)
confirmed that this appliance exposes the full tree for an actual generated
association task before native mutation qualification began.

The [actual Catalyst Center gate](profile-site-recovery-native.log) uses generated
metadata fixtures only. Switching association recovery runs inside the existing
public profile journal after project/template-bound profile creation. Later
profile updates preserve the association and server-materialized site settings;
exact profile/site, template and project cleanup is verified. The full wireless
single-tag and custom policy-cohort workflows now include both area associations
through public reconciliation, alongside definition and tag create/update.

Each eligible action injects one local preflight GET failure, retires the proven
unsent operation through the real reconciler, submits one native mutation,
retains its actual acknowledgement as Unknown, and restarts observation of that
original task and full tree. Final public InSync and repeated no-op are required.
The switching workflow passed in 93.42 seconds and recovered one association
task. The wireless single-tag workflow passed in 258.94 seconds with eight
public metadata tasks; the cohort workflow passed in 365.72 seconds with nine.
Together, eighteen proven refusals retired and eighteen original acknowledged
tasks recovered, including five profile/site association tasks. All three
workflows reached final public InSync/no-op. The full native command passed in
720.403 seconds. Both wireless workflows restore their prior metadata baselines. Existing RF
profiles remain read-only dependencies. The native APIs/tasks/readback/cleanup
are real; Kubernetes storage, interruption and preflight faults are fixtures.
Installed actors and actual process crashes are not qualified by this harness.

[Source fingerprints and terminal counts](profile-site-public-recovery-sources.json)
identify the implementation, reused manifests and immutable 0.5.0 module mapping.
[Strict documentation verification](profile-site-recovery-docs.log) covers the
operator/evidence links. No new manifest field or annotation is needed.

## Remaining full-goal work

This adds no membership ownership receipt, unassignment, prune/delete, adoption,
owner handoff or profile creator authority. Existing imported matches remain
unowned. Unknown outcomes without task IDs or fresh private refusal proofs still
need evidence-based investigation. Switching definitions retain their separate
project-binding contract; this change does not relax it.

Physical device assignment/provisioning, WLC/AP effects, large/hierarchical
association populations, installed workers and complete controller/model
qualification remain open. No software upgrade, device operation or installed
cluster repair is performed here. The resource ledger still contains all 93
pinned types; full model/schema/default/ownership/lifecycle parity and merge
readiness are not established by this association milestone.
