# Installed discovery handoff across source loss and worker restarts

`TestLiveInstalledDiscoveryHandoff` passed in **164.33 seconds** on the existing
isolated `kind-cvk-catc-qualification-20261017` cluster and the real Catalyst Center
lab. The installed manager and workers performed all journal, task, ownership,
retirement-attestation and finalizer transitions. The harness never wrote status,
called a reconciler or fabricated a task. Its separate native observer allowed
only GET and token authentication.

## Two installed incarnations

Both incarnations used the same config name, ConfigMap name, native job name and
resolved NetAsCode 0.5.0 model. They referenced the existing lab discovery's global
credential descriptions and the single target `198.51.100.101`.

| Stage | Config UID | ConfigMap UID | Native UUID |
| --- | --- | --- | --- |
| Predecessor | `35802105-7452-4e64-9e67-b0c71dbc49b2` | `412fb432-c806-4e08-aa6e-897fe7267723` | `228` |
| Successor | `6b3b4c3c-000b-4b71-b267-9d3196c2054f` | `2b92c3ed-fb12-42e4-bef7-e344ff36c163` | `233` |

Each Create naturally became Unknown under the explicit one-nanosecond test task
timeout. The harness saved its acknowledged task and restarted its exact worker
Pod using UID/resourceVersion preconditions after checking the ReplicaSet and
Deployment owner chain. Installed recovery retained the original task identity.
The predecessor lost its ConfigMap while Create was outstanding. The successor
reached InSync after restart, then lost its source before deletion.

Original Create tasks were `01a14d36-2814-7db6-9ef2-c6c3dc6c1f73` and
`01a14d37-7460-7fd8-a7b0-0da5217f3bba`. Cleanup used exact Delete tasks
`01a14d36-d753-7fa9-89c5-102f1b94bfd2` and
`01a14d38-24eb-7e9a-b5d3-24a3cf8515b9`.

The manager removed its own finalizer after verified cleanup in each cycle. A
test keeper held each deleting config for inspection; the harness removed only
that keeper after durable evidence and repeated native baseline checks. Both
configs and both source ConfigMaps disappeared. The second manifest acquired
inventory ownership automatically from the predecessor's retained attestation,
without any manual owner/receipt edit. Its final journal retains both deletion
receipts; the predecessor's complete receipt is unchanged. The latest retirement
attestation and cumulative history digest validate independently.

Repeated native reads restored the complete pre-test discovery baseline after
each cycle. Final inventory count and target UUID/serial matched the initial
snapshot. This installed test proves observed task continuity, receipt history and
effects; unlike the separate native trace test, it does not instrument the
installed transport to count every HTTP mutation attempt.

## Isolation and installation boundary

The fresh namespace is `cvk-catc-handoff-20261018`, UID
`59b068d8-003e-410c-b5cd-18a4bb778af4`. Controller `catc-handoff` has UID
`0bae4191-35bb-44ec-8379-32f103d1df7b`. Device adoption is disabled and no
CiscoDevices were present. The endpoint is `https://host.docker.internal:19443`.
A dedicated runtime API Secret was created from the user-supplied local login;
neither its values nor existing Secret/comparison-key contents were saved in
evidence. Existing manager permissions and ordinary worker namespace bindings
were used, with no ClusterRole or CRD expansion.

The Linux arm64 image uses the pinned distroless base and production source digest
`5d36bf0dfde744b2b1e5894f6f736213134f628629a50abf4be784086b357ce2`:
`cvk-catc-worker-qualification:20261018-discovery-handoff-5d36bf0dfde7`.
The qualification rollout changed only the existing manager image and its
worker-image argument with identity/old-value tests; VK image flags were preserved.
The manager reconciled both workers. Helm values still describe the older image;
this is an explicit qualification Deployment override, not a Helm-upgrade test.

The original namespace's complete retained configuration journal is unchanged,
including its unattested inventory owner and deletion receipt. Its two existing
configs remain InSync. Both original processes and the new worker are Ready.
The new controller/namespace remain available with their two receipts and valid
retirement attestation. Read-only authorization reviews confirm its worker can
update config status but cannot patch/update config metadata or finalizers.
No main-cluster restore or device software upgrade was performed.

## Remaining scope

This closes installed qualification for the bounded pure-discovery handoff flow.
Legacy owners without a retirement attestation, controller-generation changes,
mixed/grouped/template or other inventory-consumer handoff, Retain-policy removal,
history garbage collection and broader pinned-model parity remain open. The
complete handoff-source race audit subsequently passed 13,593 test/subtest events
in 1688.248 seconds with no failures ([terminal evidence](discovery-handoff-full-suite.json)).
That source predates optional-null parsing; hosted CI remains unqualified.

Private checkpoint: `/private/tmp/cvk-installed-discovery-handoff-2530688223`.
Build/rollout evidence:
`/private/tmp/cvk-installed-discovery-handoff-qualification-20261018`.
See the [sanitized validation record](discovery-installed-handoff.json) and
[test output](discovery-installed-handoff.log).
