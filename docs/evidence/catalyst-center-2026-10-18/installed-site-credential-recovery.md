# Installed site credential assignment recovery

The isolated qualification cluster now runs the source-pinned assignment recovery
implementation in its real manager and controller-worker. All six selected
credential families passed through ordinary ConfigMap intent, the existing
network-settings owner and durable Kubernetes journal against the lab appliance.
This closes the installed-actor gap from the earlier
[simulated-persistence native qualification](site-credential-recovery.md).

The live test uses the existing selected-assignment exporter and public
reconciler; no alternate assignment writer or status/phase edits were added.
For each family, a deliberately tiny 1ns task timeout produces the normal
acknowledged Unknown transition. The harness records that original operation and
task, deletes only the exact UID-fenced worker Pod, requires a new Ready Pod,
and waits for the original task to settle InSync. A later authoritative installed
observation must leave the journal idle. Independent native verification repeats
the original complete task-tree, site UUID, credential UUID and effect checks.

| Gate | Result / scope |
| --- | --- |
| Live race run | Six families passed; test 389.91s, package 392.018s |
| Worker replacement | Six distinct original Pod UIDs; all original task IDs retained |
| No-op | Later installed observation after settlement, plus independent native readback |
| Historical state | 13 action receipts and six versioned credential receipts preserved |
| Cleanup | All six generated global definitions and the one new empty area removed |
| Final runtime | Journal idle; original report config restored and InSync; zero CiscoDevices |
| Build / deployment | Linux/arm64 binary built, image loaded; isolated manager and worker available |
| Live opt-out compile | Passed with mutation flags absent |

The installed image is
`cvk-catc-worker-qualification:20261018-site-credential-recovery-2a0d8c8336a0`.
The direct-device worker image and main cluster were not changed. Existing
[API, ND and GNOI/SWIM regression gates](site-credential-recovery.md) cover the
unchanged production recovery code; this change adds the installed qualification
harness and safer fixture retention on assignment-test failure.

Fixture setup creates and rotates generated global definitions separately from
assignment execution. After each success, the installed actors first restore the
empty report source. Supervised cleanup then disables the generated-area local
slot and removes the exact generated definition; the empty area is removed only
after all six slots are cleared. An unsuccessful assignment test now retains its
definition instead of deleting a potentially referenced credential. Unknown or
failed fixtures are retained for inspection rather than replayed automatically.

No Secret API read or projected-file read is performed by the installed
assignment harness. The assigned definitions are referenced by name; generated
credential values remain in the fixture setup process. Checkpoints retain only
non-secret models, identities and status. The private comparison-key projection
used by the existing site owner is neither copied nor read.

Physical device authentication, real inventory credential synchronization,
parent-inheritance removal, credential pruning/handoff and full pinned 93-resource
parity remain incomplete. This test assigns credentials only to a new empty area;
it does not change any of the three switches.

See [source/image/task record](installed-site-credential-recovery-validation.json),
[live log](installed-site-credential-recovery-native.log),
[opt-out compile](installed-site-credential-recovery-optout.log),
[binary build](installed-site-credential-recovery-binary-build.log),
[image build](installed-site-credential-recovery-image-build.log),
[image load](installed-site-credential-recovery-image-load.log),
[Helm upgrade](installed-site-credential-recovery-helm-upgrade.log),
[worker rollout](installed-site-credential-recovery-worker-rollout.log) and
[operator contract](../../controllers/catalyst-center-credential-defaults.md).
