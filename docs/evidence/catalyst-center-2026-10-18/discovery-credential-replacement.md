# Credential-bearing discovery: owned replacement execution

Branch `pr/johalley/catc-support`; NetAsCode module pin **0.5.0**.
This stage connects the earlier retained audit/capacity work to public execution.
It does not establish native credential delivery, installed qualification or full
93-resource pinned-model parity.

## Execution boundary

The existing shared journal, submission permit, task observation, timeout and
Unknown recovery remain authoritative. The resource executor selects the latest
creator incarnation, verifies its original exact UUID/public fingerprint, then
submits one native Delete. It rechecks the full declared source, projection,
resolved identities, receipt history and capacity immediately before dispatch.
Retirement requires the successful original task and repeated complete absence
of the UUID and same-name job. A dedicated atomic status update retains the
retirement and Verified phase without a credential-delivery application.

After journal release, a contextual successor input binds the exact retirement.
Creation reuses the existing credential projection/paired completion. Every
predecessor creator, credential application and retirement remains in the audit.
Returning to previous semantic settings cannot overwrite prior applications.
Partial credential removal preserves floors; reintroduction advances counters.
Unchanged legacy v1 pairs may acquire public proof through the existing idle
promotion, but changed legacy inputs cannot invent the missing predecessor proof.

The shared finish gate accepts only complete validated retirement evidence.
Generic verification and generic credential publication remain denied. Taskless
uncertainty, lost permit acknowledgement, failed task, retained/foreign native
jobs, drift, capacity exhaustion and removed proof do not authorize a retry.
An accepted operation whose source changes stays fenced; there is no implicit
rebind or automatic cleanup under a different intent.

## Qualification cases

Public reconcile cycles use controlled native responses and either the existing
fake Kubernetes client or a real local API server. Cases cover Inline/ConfigMap
source, multiple replacements, changed username scope followed by return to an
earlier scope, partial credential removal and higher-counter reintroduction,
old-counter refusal, active owned job replacement, original deletion-task Unknown
recovery, lost accepted/receipt acknowledgements, rejected receipt publication,
pruned retirement, taskless Delete, lost permit acknowledgement, retained native
UUID, foreign same-name UUID, failed task, drift, pre-delete capacity and changed
source after an accepted Delete. Simulated restart preserves all receipts and
issues no additional operation. Test credentials are markers, never real logins.

The previous public test labelled `rotation` now deliberately leaves its Secret
projection behind the new model counter, preserving its stale-projection refusal
contract. Successful version-changing execution is tested in this new suite.

## Remaining boundaries

Native credential-bearing discovery/replacement needs actual lab device CLI/SNMP
credentials. The authenticated Catalyst Center API account is not a device login.
No synthetic creator or retirement is installed in the lab to fabricate that
qualification. Installed-worker execution, source-loss recovery, prune/delete,
removal of the final projected leaf, cross-family migration, broader composition
and other pinned-model resource gaps remain open.

See the accompanying JSON and logs for exact source fingerprints and gate results.

## Recorded gates

- Final discovery/shared-journal race and Kubernetes API regression: PASS,
  152.238 seconds, 1,492 passing test/subtest events.
- Existing SWIM/XFSU/reload regression: PASS, 2.866 seconds.
- Uncached shared contract, ND, upgrade coordination and maintenance tests: PASS.

[Machine-readable source and gate record](discovery-credential-replacement.json),
[discovery regression log](discovery-credential-replacement-regression.log),
[SWIM regression log](discovery-credential-replacement-swim.log), and
[foundation regression log](discovery-credential-replacement-foundation.log).

Complete CVK build and strict documentation validation passed. The current-source
[native read-only check](discovery-credential-replacement-native-read.log) also
passed: three devices and two SWIM images. An initial live-tag linker failure
from local disk exhaustion occurred before native execution; the exact read-only
retry passed after bounded cleanup of old generated Go cache files. This does
not qualify credential-bearing replacement or device login.

## Follow-up

[Terminal source pruning](discovery-credential-prune.md) extends this lifecycle
with explicit source removal and later-input recreation, including empty desired
sources. These earlier replacement results remain scoped to their recorded
source; they do not qualify that later stage or native credential cleanup.
