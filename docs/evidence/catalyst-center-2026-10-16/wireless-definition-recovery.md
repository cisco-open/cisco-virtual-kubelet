# Acknowledged wireless definition Update recovery

Date: 2026-10-16. Branch: `pr/johalley/catc-support`. Pinned NetAsCode 0.5.0
`a435555fe828f03cf3311d52fe1ab6572a247916`.

## Scope and shared structure

Anchor groups, interfaces, power profiles, 802.11be profiles, pre-auth ACLs and
RF profiles now resume observation of acknowledged Updates retained as Unknown.
The compiled plan selects only its actual declared resource names and exact
standalone or combined-group namespaces. An arbitrary matching prefix is not
sufficient. An existing native target ID, task ID, submission time, valid journal
ID, config identity and controller/config generations are mandatory. Physical,
credential-bearing, cancellation, preparation, dependency and created-resource
shapes are excluded from this new path. Create uncertainty remains fenced.

The feature-template Update selector now reuses the same strict structural
predicate, preserving its original section and resource scope. The existing SSID
credential/history-specific fallback remains available in mixed manifests,
including credential-projected compilation. AP credential recovery is not widened.
There is no new provider interface, worker, transport, credential store or journal.
ND and direct-device/SWIM production code are unchanged.

The common reconciler still requires unchanged controller, owner, generation and
source bindings. It reads the retained task through the existing task reader and
uses the existing Unknown → Verifying transition. Failed, pending, missing or
foreign tasks cannot complete. Existing per-resource independent readback must
match the recorded native ID and desired fields before Verified and journal
release. Resuming observation grants no submission permission and does not infer
an acknowledgement from converged native metadata. Missing source/task evidence
is not reconstructed. The generic task reader validates its existing root task
contract; this addition does not claim a new product-specific task-tree proof.

## Verification

- [Final fixture tests](wireless-definition-recovery-tests.log): race pass 8.859s.
  Six resource kinds each cover 23 operation-shape cases in standalone and mixed
  namespaces (276 scope cases). Fifty-four public reconciliation cases cover
  known-task recovery, missing task IDs, foreign/pending/failed tasks, changed
  source, replaced/missing native targets and uncertain Creates. Failed cases
  retain the journal and exact mutation counts; they do not replay.
- A combined six-collection public manifest creates six definitions, then recovers
  six serialized acknowledged Updates with twelve total writes. Simulated
  reconciler restarts and subsequent no-op passes do not add mutations. An
  explicit mixed-plan regression preserves SSID credential-history recovery and
  refuses pruned history.
- [Private API-server tests](wireless-definition-recovery-api.log): race pass
  16.723s. Twenty-four cases verify actual CRD metadata/status persistence for all
  six kinds, known recovery, missing tasks, real spec-generation changes and
  target replacement. Kubernetes 1.35 API/etcd processes are private and ephemeral;
  native services in this test are HTTP fixtures. No installed cluster is touched.
- [Wireless and shared completion regression](wireless-definition-recovery-regression.log):
  race pass 93.585s, including feature-template and SSID recovery. It preceded the
  final additional combined/fallback fixture tests above; production code did not
  change after this regression. This is not a full CC-package or installed rollout
  qualification claim.

Adapter registration, complete ND, shared software lifecycle and software-upgrade
packages also pass with race checking ([foundation log](wireless-definition-recovery-foundations.log)).

## Native public reconciliation

`CATC_WIRELESS_DEFINITION_RECOVERY=isolated-definitions` and
`go test -race -tags live ./internal/controlleradapter/catalystcenter -run '^TestLiveWirelessDefinitionRecovery$' -count=1 -v`
passes all six cases in 30.23s (package 32.577s).
[Native log](wireless-definition-recovery-native.log) and
[completion receipts](wireless-definition-recovery-complete.json) retain fixture,
native identity and Create/Update/Delete task references.

| Definition | Generated name | Recovered native Update task |
| --- | --- | --- |
| Anchor group | `CVK_DR_508628740000` | `01a1437b-3ad3-7edb-9152-75f55761b887` |
| Interface | `CVK_DR_515946619000` | `01a1437b-5036-7a60-99fd-f97f197c8f28` |
| Power profile | `CVK_DR_520495192000` | `01a1437b-61f3-7324-8e63-8bcaf235acb1` |
| 802.11be profile | `CVK_DR_525020580000` | `01a1437b-739e-7a9b-89d0-c7ac99005e7e` |
| Pre-auth ACL | `CVK_DR_529500580000` | `01a1437b-852f-7f3b-9a4c-26c39774b827` |
| RF profile | `CVK_DR_534099802000` | `01a1437b-9764-7e1e-aa19-fc6e93577409` |

Each case uses the actual client, public reconciler and native service with
simulated Kubernetes persistence. Acknowledged Update evidence is deliberately
retained as Unknown and JSON-round-tripped before a new reconciler instance
observes it. The original target/task identity remains bound. Native readback and
three no-op passes reach InSync. A request guard permits only one POST to the
absent generated name, one PUT to its captured ID and one explicitly enabled
DELETE of that ID. It counts every attempted mutation, including denied attempts.
Each case finishes with exactly three mutation attempts and restoration of the
original collection inventory as projected by its existing typed reader. This
does not attest unmapped native field values. All six fixtures are removed.

The ACL fixture sets explicit nonzero source ports, preserving the existing
appliance compatibility limit rather than silently changing pinned defaults.
Two preliminary anchor attempts stopped on a harness resource-version conflict
between source revisions, after Create and before Update. Both exact unused
fixtures were separately verified and removed (cleanup tasks
`01a14378-a759-741c-82e8-efa48f366206` and
`01a1437a-609a-7b55-b13c-82ff7fd56364`). The final harness refreshes current
metadata before every desired-state revision and persists created identity evidence.
No preliminary fixture remains.

## Remaining work

This closes bounded acknowledged Update recovery for these six definition kinds.
It does not add Create ownership, taskless recovery, adoption, pruning/CR deletion,
physical WLC/AP associations or deployment. Installed worker/actor qualification
and broader field/default semantics remain pending. Full 93-resource parity is
still incomplete; no whole-resource/section support flag changes.
