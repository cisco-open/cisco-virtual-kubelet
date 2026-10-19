# Public wired bulk recovery network evidence

Retained wired bulk Verifying operations now invoke the existing declared-network
worker comparison through public acknowledged-group recovery. The adapter
reconstructs the original batch, repeats native task/member/dependency checks,
then delegates query generation, manager-bound DeviceOperation collection and
semantic comparison to the existing wired helpers. No alternate transport,
worker, reservation protocol, writer, RBAC grant or ownership store was added.

Public status reports `GroupDeviceEvidencePending` while fresh results are
missing. Matching supported network fields still report
`GroupConvergenceVerificationUnavailable`: these checks do not prove switching
templates, credentials or the full rendered provisioning footprint. Mismatched,
incomplete, redacted, stale or otherwise invalid evidence remains fenced. No
completion receipt is published, member reservation released or new provisioning
write enabled. Unsupported/dependency-only context does not create empty proof
requests.

The existing semantic helpers support scoped NTP, DNS, custom MOTD banners and
fixed UTC timezone observations. Public-route scenarios cover NTP and mixed
banner/timezone matching and refusal; the existing CLI regression covers DNS and
other scalar semantics. Queries begin with chassis identity and reuse all-member
source/owner/native/managed-identity checks around the batch. Accepted/Unknown
recovery and changed/removed grouping-option safeguards remain in place.

| Gate | Result / boundary |
| --- | --- |
| Final race regression | 14 families / 159 subcases; 30.610s |
| Focused actual public route/status | 10 cases; 11.594s; native HTTPS and Kubernetes actors simulated |
| Private API | Three existing device-evidence envelope tests; 19.457s; not a full public batch/actor qualification |
| Native lab read-only context | 18.714s; three switch identities and timezone context, zero native writes, no device outputs or convergence claim |
| ND | 4.952s |
| GNOI/SWIM | 6.435s |
| Production build | Passed; new binary not installed |

The first public test failed because its authority fixture attempted to configure
an already-configured reservation runtime. The corrected fixture verifies and
preserves the existing group protocol/lease namespace. The production one-time
configuration guard is unchanged; the failed controlled-fixture log is retained
separately.

The isolated cluster currently has zero CiscoDevices, so installed worker/device
qualification is pending. Its existing image predates this new public wiring.
Complete provisioning semantic proof, public new dispatch, atomic completion and
reservation release remain pending; this milestone is not full wired provisioning
or pinned-model parity.

See [source hashes and validation scope](wired-bulk-recovery-network-validation.json),
[final regression](wired-bulk-recovery-network-regression.log),
[public route](wired-bulk-recovery-network-public.log),
[private API](wired-bulk-recovery-network-api.log),
[lab read-only](wired-bulk-recovery-network-native-readonly.log),
[ND](wired-bulk-recovery-network-nd.log),
[upgrades](wired-bulk-recovery-network-upgrades.log),
[build](wired-bulk-recovery-network-build.log) and
[initial fixture failure](wired-bulk-recovery-network-initial-public.log).
