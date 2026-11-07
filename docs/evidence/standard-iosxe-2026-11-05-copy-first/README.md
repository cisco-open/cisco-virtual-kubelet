# Standard failed-download fix: C9K requalification, 5–6 November 2026

This run evaluates the revised Standard candidate against the preserved
[stale-runtime failure](../standard-iosxe-2026-11-05-intermediate-crl-refresh/README.md).
The earlier result and raw evidence are unchanged. Standard remains the default;
Verified is optional and Governed is deferred. No artifact metadata, installation
journal or grant is configured.

| Boundary | Result (UTC) |
| --- | --- |
| Revocation published | 2026-11-05T20:51:48.178429+00:00 |
| Cached CRL NextUpdate | 2026-11-05T21:00:28+00:00 |
| C9K fetched revoked CRL | 2026-11-05T21:00:36.426890+00:00 |
| Native serial-specific revocation error | 2026-11-05T21:00:36.436000+00:00 |
| Fresh image while old CRL was cached | fresh-image-accepted-using-cached-unrevoked-CRL |
| Failed-copy boundary | No fixture configuration, runtime, Ready or package GET |
| Current revoked CRL renewed before continuation | 2026-11-06T07:14:11.444380+00:00 |
| Worker restart while revoked | Block remained in force; same canonical Lease |
| Certificate-only repair | Same Pod recovered the expected release after natural fence |
| Fresh HTTPS with IfNotPresent | Exact compiled release confirmed |
| Preloaded local Never; origin offline | Exact release confirmed before and after worker restart |

## Implementation and reproduction

The previous flow passed the remote URL to app-hosting install and trusted
`DEPLOYED`/`RUNNING`. On this device a failed download could still produce native
RUNNING from older package data. That bypassed the copy fallback entirely. The
fallback also attempted unchecked cache filenames before a fresh download.

The revised driver copies remote packages first using the existing synchronous
RESTCONF `Cisco-IOS-XE-rpc:copy` operation. It checks the HTTP response and the
qualified `RPC request successful` output. Missing, failed, queued or unrecognized
results stop the operation. Only a completed copy permits app configuration and
installation from the explicit device-local path. Recovery reconciliation uses
the same transfer gate; no URL install or blind cache-filename install remains.
Local references retain their preloaded workflow.

New remote installations under both Always and IfNotPresent require a completed
copy. Existing app lifecycle reconciliation is preserved. Standard does not
identify or repair an already-running wrong image during worker upgrade; roll out
a new Pod to replace an affected installation. Offline deployment uses a local
reference with Never. The default destination is `flash:/cvk-<app-id>.tar`, which
requires no pre-existing directory. Custom destination parents must exist.

The first lab candidate still used the legacy `flash:/virtual-kubelet` parent,
which was absent. Its valid-origin copy returned HTTP 400 invalid-value and
correctly stopped before app configuration or runtime. The complete
[initial attempt](initial-missing-directory/PRESERVED.json), original candidate,
origin events and worker log are retained. After fixing the default path, the
candidate was rebuilt, the full race suite rerun, and the managed workers updated.
The original 31-minute fence expired naturally before rearming; no Lease was
edited or deleted. Its trustpoint was removed after normal workload deletion;
initial CRL cache entries remained in place until final cleanup.

The main actor subsequently sealed a five-minute rejection result, then stopped
when its direct `kubectl rollout restart` was rejected by reserved-worker
admission. This was a harness error; the policy was retained. The original
`qualification.log` and its hash are preserved. Continuation uses a CiscoDevice
log-level change and normal manager reconciliation to replace the worker, as in
the earlier Standard controls. The same Pod and canonical Lease survive.

During the pause, CRL 2 expired. Before further recovery checks, the continuation
published current CRL 3 carrying the same revoked intermediate serial, without
changing the certificate or clearing caches. It then required a new native
retrieval and blocked copy under that current list. Only afterward did repair
replace the served intermediate certificate; CRL 3 and all other identity,
payload and policy inputs stayed fixed during repair. Retained fences expire
naturally. The renewed negative result, manager replacements, original stop and
continuation source are retained separately; the first discovery result is never
rewritten as an uninterrupted pass.

## Device checks

The test uses C9300-24UX `FCW2247C1AJ`, IOS-XE 17.18.1 fc2, at 10.1.1.1 and an
isolated managed cluster on ux-agx. Direct RESTCONF TLS and the SSH host key are
verified. Management HTTPS temporarily uses the existing qualified certificate;
client identity trustpoint IRIS remains selected. No device clock, NTP, global
signature policy or native CRL-cache policy is changed. Only a run-owned root is
trusted; the intermediate is supplied by the TLS server.

Three independently compiled image configurations and layers identify the valid,
cached-window and refresh releases. Their immutable provenance and host smoke
checks establish the expected endpoint response without priming the C9K. Earlier
IOx image caches are retained. The positive deployment must copy the full package
and serve its exact compiled release before revocation is published.

Revocation changes only the signed root-issued list at the same CDP. The original
intermediate remains served; a separate, unchanged intermediate-issued empty CRL
covers the valid leaf. Offline controls verify intermediate revocation at depth
one. Natural cache expiry and a fresh image trigger new device validation. The
negative window requires serial-specific native PKI evidence and failed copy,
zero target package GETs, no fixture configuration, no native runtime, no Ready,
and no release response. Worker replacement must preserve this block and the
canonical mutation Lease. Observation is sustained for at least five minutes.

Repair replaces only the served intermediate certificate, retaining its subject
and key with a new unrevoked serial. This models supersession, not CA-key
compromise. Root, leaf, URLs, package bytes, CRLs and policy remain unchanged.
Recovery retains the original Pod and waits for its untouched fence before
proving the expected runtime release. A new IfNotPresent deployment independently
requires a new successful delivery. A preloaded local Never deployment runs with
both origin listeners stopped, then survives managed worker replacement without
a package request. All fixture workloads are normally deleted.

## Evidence, checks and limits

[qualification.json](qualification.json) independently assesses the signed lists,
certificates, native cache, origin hashes, compiled releases, workload status,
worker identities, leases and cleanup. [candidate.json](candidate.json) identifies
the dirty source fingerprint, Linux binary, image configuration and imported
manifest. [implementation.patch](implementation.patch) isolates this task's code
changes from earlier workspace work. [test-results.json](test-results.json)
records the final `go test -race ./...` result for all 62 tested packages.
Repeated observations are retained in the indexed compressed snapshot archive.
SHA256SUMS covers all exported files. Credentials, kubeconfig, private keys and
image binaries are excluded from the public evidence.

This is a bounded Standard transfer qualification. It does not turn copy
completion into cryptographic package attestation, validate every IOS-XE release,
prove absence of all firmware cache defects, or make CRL discovery immediate.
The observed cached-list window remains part of device TLS policy. Unavailable
or stale CRLs, OCSP and optional cache-policy overrides are outside this run.
Use optional Verified for signed artifact identity, on-device checksum checks
and durable installation recovery. Previously installed Standard apps are not
automatically evicted or reidentified.

Final cleanup restores the original HTTPS selector and removes only run-owned
apps, files, roots, lab cluster, origins and fixture tags. A CRL cache clear occurs
only after testing and worker stop, and only if every cached entry is proven to
belong to this run, including the preserved initial attempt. No global image
cache prune, manual Lease/journal reset, new transport or Governed work occurs.
Unrelated iris RUNNING, c9ktest DEPLOYED, CAF Stable, client IRIS and the original
signature policy must remain intact. Earlier raw evidence hashes and historical
export manifests are independently verified.
