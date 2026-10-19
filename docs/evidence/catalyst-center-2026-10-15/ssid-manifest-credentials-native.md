# Native SSID credential manifest qualification

## Qualified flow

The existing repository `wireless-ssid-credentials-apply.yaml` example now has a
native qualification through the public configuration reconciler. The test reads
that example's Spec/model/SecretRefs, changes only the isolated name/controller
reference and credential version counters, and uses the common projected-secret
path/atomic JSON format. Generated passwords stay in temporary projection files
and memory, never in the model, journal, log or evidence artifacts.

A random absent Global SSID remains disabled with WPA2_PERSONAL/PSK/CCMP and
5 GHz intent. A transport guard allows only one matching create, one update to
the captured native ID and one exact delete; profile/device actions and repeated
mutations are refused. Native task receipts, public metadata and durable
credential versions are correlated by the existing journal. JSON reload and a
rebuilt adapter between passes exercise restart/no-replay. Version 2 with the
old projection is refused without an HTTP mutation; replacing the projection
then completes a same-ID credential rotation. Repeated convergence is a no-op.

The journal ends with no active operation and a credential application receipt
for version 2, semantic key `passphrase` and source path `/ssids/0/passphrase`.
Generated values are checked against persisted status and projected public
metadata throughout the run. The test checks unchanged profile and unrelated
SSID baselines before each pass and repeated exact absence after delete task
completion. Delete task identity/outcome is retained privately; DELETE is never
retried. This supervised fixture cleanup does not advertise public prune/delete.

## Native results

The final repository-example qualification passed in **15.72 seconds** (package
17.526 seconds). Fixture name was `CVK_PSK_92e4f32c026bfd8e`, native ID
`mdi0q1zl-x1bt-s185-mMu0-zJmYyZaYnMjMzdHLqZbwmuSYxZnq`, Global site
`8995b7da-5b82-40fd-a706-82150affe92d`. Exactly one POST, PUT and DELETE were
observed. Delete task `01a1413b-6089-709d-9592-5b515269d84b` completed and absence
was independently verified. Initial/final SSID and profile baselines were empty.
Private metadata/journal/source/baseline/cleanup evidence is at
`/private/tmp/cvk-ssid-manifest-3647107155/` (directory 700, files 600). Projected
passwords were temporary and were not copied into that recovery directory.

An earlier public credential run passed in 17.76 seconds (package 19.891), and
the first run consuming the repository example passed in 17.70 seconds (package
19.833). The final run above additionally exercises strengthened explicit
post-delete absence checks and saved cleanup-task evidence. SSID credential/
projection/version/ownership regression tests passed in 3.303 seconds; the final
live-tag build passed in 2.145 seconds without executing mutation tests.
Production foundation/ND/direct-device/SWIM code was unchanged in this stage.

## Discovered qualification issue and recovery

The initial harness stopped after an acknowledged update because the appliance
temporarily omitted that SSID from inventory while the task was in progress.
No production workaround or replay was introduced. The harness now tolerates
absence only for the exact retained Accepted/Verifying update, native ID, site
and task. Unknown/uncorrelated absence and changed IDs remain refused. The
existing reconciler waited for the real task/readback and completed correctly.

The original update task `01a14130-7e53-71dd-b185-3e2ec6ef6dde` completed with the
original native ID. Exact retained recovery verified ownership, disabled state,
no profile references and stable repeated inventories, then removed only
`CVK_PSK_3b202f4b08882ac8` / native ID
`mdi0q1zl-x1bt-s18Z-yJiW-mMy0yJa4odGYywm4qZbwmuSYxZnq` using delete task
`01a14133-2d42-79e5-b514-9906e186983e`. Cleanup passed in 2.56 seconds (package
4.679) without recreating/updating the definition or reconstructing discarded
passwords. Recovery metadata remains at
`/private/tmp/cvk-ssid-manifest-3046175533/`, including the separately recorded
verified cleanup outcome. A subsequent read-only native inventory test confirmed
zero Global SSIDs in 0.51 seconds (package 2.571). No fixture remains retained in
Catalyst Center from these runs.

## Remaining scope

The Native API, task outcomes, projection files and public reconciler are real;
Kubernetes object persistence is simulated. This does not qualify installed
worker Pods, real projected-volume rotation, RBAC/admission or CRD status
preservation. The compiled journal types are reloaded through JSON; that is not
a substitute for installed schema rollout.

This is a disabled, unassociated logical credential definition. Correlated task,
public metadata and version receipts cannot attest a write-only password's
actual value or client authentication. No WLC/AP association, provisioning,
physical switch mutation, site claim or upgrade was submitted. Multi-PSK native
combinations, broader SSID field combinations, adoption, pruning and installed
worker/device authentication remain pending. Full pinned NetAsCode execution
parity is not complete.


The subsequent [Multi-PSK qualification](ssid-multipsk-native.md) now covers main
plus two ASCII keys, priority reordering without mutation and one-key rotation
through the public reconciler/native task path. Broader combinations and
installed worker/device authentication remain pending.
