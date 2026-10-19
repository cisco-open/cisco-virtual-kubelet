# Installed discovery recovery after source loss

`TestLiveInstalledDiscoverySourceRecovery` passed in 71.73 seconds using the
existing isolated `kind-cvk-catc-qualification-20261017` cluster and the real lab
Catalyst Center. The harness never called the adapter's reconciler or wrote
status: the installed manager and worker performed enrollment, scope attestation,
task observation, exact native cleanup, completion proof and finalizer release.
Its native observer transport allowed GET and token authentication only.

The generated single-address discovery used the existing lab's global credential
descriptions and target `198.51.100.101`. The test did not read Kubernetes Secrets
or projected comparison keys. A one-nanosecond task timeout caused a naturally
acknowledged Create to become Unknown. The harness then deleted its ConfigMap and
requested config deletion, with UID/resourceVersion preconditions, and restarted
the exact worker Pod after checking its ReplicaSet/Deployment owner chain.

The replacement worker recovered original task
`01a14d13-089f-7a1c-9999-f54a69ac3b5b`, retaining operation
`cb5b41ddc83a06ad863a12d5dc500072`. It established owned job **203**, then removed
that UUID through cleanup task `01a14d13-9be8-7f06-8e26-76a92d0e29a1`.
The manager released only its cleanup finalizer. The test inspected the retained
deletion receipt and completion status before removing its own keeper finalizer;
the config then disappeared. No status faults or outstanding tasks were fabricated.

Two native reads matched the complete pre-test discovery baseline. Inventory count
and the target's UUID/serial were preserved. Other sections' complete journal
evidence matched its pre-test snapshot. Both existing configs remained InSync and
both installed processes were Ready after the test.
Read-only authorization reviews confirmed the installed worker can update config
status but cannot patch or update config metadata, including finalizers.

## Installation boundary

The image was built from production source digest
`3dd37f0d780c790b4f537874bff1687ecc0f82fd48be88a5212f4052a308124d` using a pinned
distroless base and Linux arm64 binary. Image ID:
`sha256:cc03b6f2786557726de72d9fecf18973bdb826cb7d3d242469cfe105d002da11`.

The qualification-only rollout patched the exact manager Deployment's image and
worker-image command argument with UID/resourceVersion and old-value tests.
The manager reconciled its worker. VK image arguments, CRDs, RBAC and existing
config specs were not changed. Both processes retain the qualified image:
`cvk-catc-worker-qualification:20261018-discovery-source-3dd37f0d780c`.
This is an explicit Deployment override; Helm release values still describe the
older image. It is not a Helm upgrade qualification or a main-cluster restore.

## Remaining scope

The inventory owner and exact deletion tombstone deliberately remain after CR
deletion. Automatic owner retirement/handoff is not implemented; another config
UID cannot reuse that section. This test does not qualify unattested legacy source
loss, paused/changed controllers, mixed/grouped deletion, taskless uncertainty or
full pinned-model parity. The broad non-live audit is tracked separately until it
has a terminal result; this live pass does not substitute for that gate.

Private checkpoint: `/private/tmp/cvk-installed-discovery-source-709168992`.
Build/rollout logs:
`/private/tmp/cvk-installed-discovery-source-qualification-20261018`.
Sanitized [validation record](discovery-installed-source-recovery.json) and
[test output](discovery-installed-source-recovery.log) preserve the qualification.
