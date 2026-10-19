# Switching profile original-task recovery

Pinned NetAsCode **0.5.0**, revision
`a435555fe828f03cf3311d52fe1ab6572a247916`, branch
`pr/johalley/catc-support`. Full resource and model parity remain incomplete.

## Execution change

Switching profile definitions now join the existing network-profile metadata
observer. Supported acknowledged Create/Update operations retain the original
journal, source/controller generations, section owner, declared profile name,
Update target UUID and project-aware dependency binding. Observation uses the
original task and complete rooted task tree; it never submits another request.
Ordinary Accepted switching tasks also require complete task-tree success before
entering native verification.

Only a declared switching executor in the existing `profiles/` namespace is
selected. Slash-containing names remain literal names. New `sp2-` bindings retain
template membership and original project/template UUIDs. The exact legacy
empty-membership digest remains observable; other legacy hashes cannot prove
original project lineage and remain fenced. Taskless, foreign-input/owner,
physical and credential-bearing envelopes do not acquire recovery authority.

Unknown recovery compares selected native observations before and after original
task completion. Canonical template sets and inherited attributes use the same
private settings reader as submission guards. Unique-key attribute ordering is
normalized, opaque values and numeric precision remain exact, and a changed
selected UUID or inherited value during observation refuses completion. Inherited
values stay in memory and never enter status, digests, errors or evidence.

Recognized switching writes also reuse the existing composition-guard proof
whitelist. Only a genuine one-use outer guard refusal, issued before the original
submit function runs, may retire the unsubmitted journal through the existing
CAS. The shared proof matrix covers both switching Create and Update, including
foreign/reused proofs, changed owner/parent/generation, recorded tasks/receipts,
conflicts and ordinary/lost-acknowledgement errors. The inner switching selected-state
guard does not yet issue this proof; its taskless uncertainty remains fenced.

Normal verification and journal release remain unchanged. A successful Create
task and stable current matching definition prove the existing Retain convergence
contract; they do not establish which native UUID that task created. This adds no
creator custody, adoption, prune or deletion authority.

## Verification

Public HTTPS fixtures and a real temporary Kubernetes API server exercise Create
and Update recovery, original project/template replacement refusal, missing task,
changed source/owner, successful-root/failed-or-pending-child refusal, unavailable
or foreign task trees, inherited numeric changes during task observation,
semantic attribute reordering, literal slash names and legacy empty/nonempty
bindings. Update cases also cover target replacement before and during the tree
read. JSON persistence and reconstructed reconcilers preserve the original
operation without replay. Private fixture values are asserted absent from the
persisted controller journal.

The initial fixture placed a numeric value in a field whose API type is string.
It now uses a valid opaque numeric extension and removes the old inherited group
before supplying its current detail view, preserving duplicate-group rejection.
No production parser or verification check was relaxed.

The temporary real Kubernetes API server passes all 36 cases. A separate native
public-reconciler cycle recovers one Create and three Update tasks from Unknown,
reaches InSync for each source revision and proves restarted no-ops. It also
assigns the generated profile to an empty generated area and verifies membership
removal/restoration after assignment while preserving inherited settings. Exact
profile/assignment/area, template and project cleanup completes. No real device,
AP or template deployment is targeted. Native Kubernetes persistence is simulated;
this is not installed-worker qualification.

The focused race gate passes 361 test events; the separate switching composition
proof matrix passes 35. The broad regression passes 19,745 events across sixteen
packages, including ND, shared configuration, maintenance, direct-device and SWIM
paths. The production worker and strict documentation builds pass.

Final results, task identifiers, source fingerprints and log hashes are recorded
in [the machine-readable evidence](switching-profile-task-recovery.json).

## Remaining scope

Installed worker/source projection and recovery, broader onboarding/composite and
multi-site combinations, resource ownership/adoption/prune, guard-certified automatic no-dispatch retirement,
and correlation of native Create results to creator custody remain pending. Taskless outcomes and
unbound nonempty legacy project lineage remain fenced. No complete-resource,
whole-section or full-model capability is advertised by this change.

A subsequent [inner-guard qualification](switching-profile-no-dispatch.md) closes
the automatic preflight-retirement gap recorded above. Older uncertain outcomes
still lack an original guard proof and remain fenced.
