# Standard copy-first app upgrades and rollback: C9K, 6 November 2026

This run qualifies app-hosting Deployment upgrades and history rollback on the
[copy-first candidate](../standard-iosxe-2026-11-05-copy-first/README.md).
Production source and the qualified executable/image are unchanged. Standard is
the omitted/default mode; no artifact metadata, descriptor, installation journal
or journal grant is configured. Governed remains deferred.

| Case | Release | Pod UID | Deployment revision |
| --- | --- | --- | --- |
| initial-a | STD-UPGRADE-A-20261106-8f0d26f6c543 | 257e4a85-d38b-4f10-b308-625b1e837360 | 1 |
| upgrade-b | STD-UPGRADE-B-20261106-8f0d26f6c543 | 92d6c115-029f-423e-b178-0e7e78c01f5b | 2 |
| rollback-a | STD-UPGRADE-A-20261106-8f0d26f6c543 | 5d78c63f-63ba-45a0-9f57-c732e3cd8549 | 3 |
| failed-upgrade-rollback-a | STD-UPGRADE-A-20261106-8f0d26f6c543 | cc21816b-03f5-4484-8fd2-56056836cbd4 | 5 |
| retry-upgrade-b | STD-UPGRADE-B-20261106-8f0d26f6c543 | 593bc867-5856-4688-8003-bb8cae0b7ada | 6 |
| final-rollback-a | STD-UPGRADE-A-20261106-8f0d26f6c543 | 351ffc54-c05c-4c97-b55b-c8d445f019ac | 7 |

The failed B Pod was `667ca5e8-001e-4cf6-8b65-484ddedf68e4`. Its HTTPS URL returned HTTP 404 with the
same valid certificate and unchanged package bytes. Both A and B had already run,
and the global device image cache was not pruned. Its private contents were not
inspected; previously executed images define the warm-image scenario here. The failed copy produced no fixture
configuration, native runtime, Ready state or successful package delivery. It did
not start an older cached release.

Rollback after this failure used `kubectl rollout undo` at
`2026-11-06T08:40:33.654857+00:00`, targeting the prior healthy A history revision. The
original canonical mutation fence expired naturally at `2026-11-06T09:06:15.551917+00:00`.
The new A Pod then completed a fresh copy and served the exact A release at
`2026-11-06T09:07:57.080356+00:00`, 1643.4 seconds after rollback was requested.
B remained unavailable during this rollback. After restoring only B's HTTP
availability, a subsequent upgrade to the unchanged B package and another
history rollback to A both passed.

## What was qualified

The target is the real C9300-24UX `FCW2247C1AJ`, IOS-XE 17.18.1 fc2 at 10.1.1.1,
using an isolated managed CVK cluster on ux-agx. Direct RESTCONF TLS and the SSH
host key are verified. HTTPS delivery trusts a fresh run-owned root and a valid
correct-IP leaf; the injected failure is an HTTP 404, not a certificate error.
The temporary origin trustpoint uses the ordinary no-CDP test profile. Device
clock/NTP, unrelated trustpoints, client identity IRIS, signature policy and CRL
cache policy are unchanged. No global image or CRL cache purge is performed.

Two independently compiled images have distinct Docker configuration IDs,
layers and release responses. The fixture tar bytes remain fixed throughout.
Host-only smoke checks validate them before device use. All successful
transitions require a new Pod UID, complete package transfer, driver trace showing
copy completion before local-path installation, native RUNNING, exact HTTP
release, Ready, and an Available/Progressing Deployment at its observed
new generation. Retired fixture native apps must be absent. Top-level Pod failure
text must be empty after success.

The same Deployment object is retained throughout. Initial A → B is an ordinary
image update. B → A, failed-B → A and the final B → A rollback use actual
Deployment history and `kubectl rollout undo --to-revision`; saved ReplicaSet
history proves which A template was selected. This does not simulate rollback by
manually installing a tar or replacing the Deployment object.

After both images have run, only B's HTTPS availability changes to 404. The
failure window records the unsuccessful copy, status and native configuration;
no install request is accepted for the failed Pod. Rollback is requested while
its original fence is still held. Observations prove the Lease UID, holder and
renewal time stay unchanged, and no fixture configuration, runtime, Ready or
package request appears before natural expiry. No Lease or journal is manually
edited or deleted. The replacement A Pod obtains a new complete download after
the fence, with A already previously executed and B's continuing 404 response.

## Availability and scope

This lab uses **Recreate and one fixture slot**. The old healthy app is removed
before the replacement starts. A failed upgrade therefore creates an availability
gap, and safe rollback waits for the existing mutation fence. This is not a
zero-downtime or RollingUpdate qualification. Production rollout strategy needs
its own device-capacity and availability validation. No automatic rollback,
vulnerability scanner, CVE-based version selector or Governed permission service
is introduced by this test.

The flow exercises app-hosting Docker archives through the existing synchronous
RESTCONF copy RPC, followed by device-local app installation and lifecycle RPCs.
It does not upgrade IOS-XE software. Standard still relies on the device's copy
completion contract and ordinary source trust; this run's independent package
hashes and compiled release probes are qualification evidence, not newly required
Standard artifact metadata. Optional Verified supplies signed artifact identity,
on-device checksum verification and a durable installation journal.

Deployment-history undo restores the former image URL. Retain versioned packages
at stable URLs for rollback; the test kept A available while B returned 404.
Standard's new remote installs require a successful copy even for a previously
executed image. History alone cannot recover the intended bytes from a mutable
or unavailable URL.

The [retired staging-file observation](retired-staging-observation.json) also found
three 5,959,680-byte tar files still present on flash after their native apps had
been removed. The qualification's cleanup removed those test-owned files after
all tests. Native app deletion currently preserves staging tar files; automatic
flash retention/garbage collection is not qualified by this run. This is separate
from the uninspected private runtime image cache.

## Evidence and preservation

[qualification.json](qualification.json) independently assesses the exact image
bytes, actual releases, source/image identity, Deployment/ReplicaSet history,
copy-before-install traces, HTTP 404 failure, canonical fence and cleanup.
[candidate.json](candidate.json) identifies the retained candidate, including its
dirty source fingerprint, Linux binary, Docker image ID and imported manifest.
The prior candidate's [race test results](../standard-iosxe-2026-11-05-copy-first/test-results.json)
remain applicable because production source is unchanged. Documentation and
all historical evidence manifests are checked separately.

Repeated sanitized observations are retained in an indexed compressed archive;
boundary snapshots, traces and rollout histories remain directly accessible.
SHA256SUMS covers all exported files. Credentials, kubeconfig, private keys and
image binaries are excluded. Earlier raw evidence is hashed before setup and
verified afterward without rewriting it.

Final cleanup normally deletes fixture apps, releases the mutation Lease,
removes only owned flash files, the temporary origin trustpoint, fixture tags,
origin service and isolated cluster, and deletes both ephemeral private keys.
The original HTTPS selector is restored explicitly. iris RUNNING, c9ktest
DEPLOYED, CAF Stable, client IRIS and the original signature policy are preserved;
all lab listeners close and the shared host Kubernetes node remains Ready.
