# Standard HTTPS leaf-certificate dates — C9K qualification

This run qualifies rejection of expired and not-yet-valid leaf certificates,
followed by certificate-only repair and normal deployment recovery, on
C9300-24UX / IOS-XE 17.18.1 fc2 (serial FCW2247C1AJ), 3 November 2026.

| Leaf condition | Native TLS rejections | Package GETs while invalid | Same Pod recovered | Normal deletion |
| --- | --- | --- | --- | --- |
| Expired | 2 | 0 | Yes | Passed |
| Not yet valid | 2 | 0 | Yes | Passed |

The [independent assessment](qualification.json) records the actual outcomes,
observation counts and recovery identities. Standard remains the default;
Governed remains deferred. No production code changed for this qualification.

The [CiscoDevice input](device-input.yaml) omitted appArtifacts. The
[valid](deployment-valid-input.yaml), [expired](deployment-expired-input.yaml)
and [not-yet-valid](deployment-future-input.yaml) deployment inputs supplied
ordinary HTTPS package URLs without artifact annotations or metadata. Always
forced each fresh Pod/app and distinct URL to exercise delivery. Live worker
configuration, runtime options and cluster resource inventories confirmed no
artifact journal or grant before, during or after either case. Existing
installation mutation safeguards remained active.

All three leaves used the same newly trusted root, correct IP SAN for
192.168.129.111, RSA-2048, SHA256 and serverAuth usage. Only the leaf validity
window differed. The root remained valid throughout. The
[PKI manifest](pki-manifest.json) and public certificates retain exact validity
bounds and fingerprints. Independent [TLS probes](independent-tls-controls.json)
accepted the valid leaf and rejected expiry (verification code 10) and a future
notBefore (code 9). Five [offline checks](offline-certificate-controls.json)
verified the intended failures and validated both negative leaves at a time
inside their own validity windows. No device clock, NTP or time configuration
was changed.

Native show clock detail was measured against host UTC before deployment,
at each invalid boundary and immediately after certificate replacement.
The seven clock records confirm NTP time, a valid root, an expired original
leaf and an original future leaf still before notBefore. After repair only
the active endpoint's leaf became valid: recovery was not caused by advancing
the clock or aging into a validity window.

The actual C9K valid control first downloaded the identical 5,963,776-byte
package, reached native RUNNING, Pod Ready and HTTP release A, and satisfied
Deployment Available/Progressing. The [server trace](server-events.jsonl)
distinguishes C9K client 10.1.1.1 from host-side controls. Servers offered the
same package bytes under all certificate modes and requested no client
certificate. Negative observations covered the native three-minute pull and
existing fallback, requiring sustained no Ready/RUNNING, no package GET or
transfer and actual native TLS rejection. Worker diagnostics and native app
snapshots correlate those outcomes. Acceptance under an invalid certificate
would fail rejection qualification rather than be hidden by the harness.

After sealing each invalid outcome, only the endpoint certificate was replaced
with the previously qualified valid certificate. URL, package payload and Pod
template remained unchanged. The original Pod/app identity and canonical Lease
were observed through recovery. No Lease or journal was cleared, recreated or
manually updated: the retained installation fence expired naturally before
normal retry. Recovery reached RUNNING, Ready, HTTP release A and Deployment
availability. Normal deletion finished before the next independent case.
See per-case outcomes, certificate-correction records, guarded observations,
recovery results and normal-cleanup snapshots for the measured sequence.

Preparation initially used an incorrect read-only RESTCONF readiness path and
received 404 after native setup had completed. The corrected hostname read
returned 200 with verified TLS before deployment started. No native setup
mutation was repeated. The original [preparation log](prepare.log),
[observer note](preparation-observer-note.json),
[continuation result](preparation-readiness-continuation.json) and both helper
versions are retained rather than concealing this harness error.

The [candidate](candidate.json) is the unchanged executable/image used for the
previous [issuer/hostname run](../standard-iosxe-2026-11-03-certificate-rejection/README.md)
and [HTTPS/local control](../standard-iosxe-2026-11-03-https-local/README.md).
Live manager and worker image IDs were checked. Independent qualification
checksums do not add an artifact requirement to Standard. The prior full Go
race run was not repeated for this evidence/documentation-only work; see
[validation results](automated-checks.json) for this run's checks.

[Final cleanup](final-cleanup.json) removed run-owned package caches, temporary
CA trustpoint, three listeners, isolated cluster and four ephemeral TLS private
keys. The original explicit self-signed HTTPS selector and IRIS client
trustpoint were restored. iris remained RUNNING, c9ktest DEPLOYED, CAF Stable,
and the shared Kubernetes node Ready. Native package-signature policy was
disabled at baseline and unchanged.

Scope is this device, firmware, package and configuration. Root/intermediate
expiry, revocation, chain variations, redirects, TLS downgrade, mTLS and
registry authentication were not tested. Untrusted issuers and hostname
mismatches were qualified separately, not repeated here. No artifact producer
validation, Governed workflow, uninterrupted availability, stateful migration
or specific CVE remediation is claimed. Certificate repair remains subject to
the ordinary retained safety fence; it is not immediate recovery.

Evidence is allowlisted and sanitized. It excludes Secret data, credentials,
private keys, kubeconfig and unrelated topology/interface observations. Executed
harness source and the corrected readiness helper are retained under harness/.
SHA256SUMS covers every exported file.
