# Standard HTTPS certificate rejection — C9K qualification

Both invalid-certificate rejection cases passed.
Tested on 3 November 2026 on C9300-24UX / IOS-XE 17.18.1 fc2, serial
FCW2247C1AJ. Standard remains the default; Governed remains deferred.
The production candidate was unchanged throughout this qualification.

| Certificate condition | Actual C9K outcome | TLS rejection alerts | Package GETs while invalid | Invalid-phase observations |
| --- | --- | --- | --- | --- |
| Untrusted CA; matching IP SAN | Rejected | 2 | 0 | 27 |
| Trusted CA; mismatched hostname | Rejected | 2 | 0 | 27 |

The [CiscoDevice input](device-input.yaml) omitted appArtifacts entirely. All
[deployment inputs](deployment-valid-input.yaml) omitted Pod-template annotations,
artifact descriptors and signatures. The cases used ordinary HTTPS image URLs
and the existing Always pull policy so a cached archive could not conceal the
certificate condition. Each case had a fresh Pod/app and distinct URL. Live
configuration, worker options and isolated ConfigMap/Role/RoleBinding inventories
confirmed that no artifact journal or journal grant existed before, during or
after the tests. Normal worker configuration and mutation safeguards stayed in use.

Fresh trusted and untrusted roots were generated for this run. Only the trusted
root was imported into the temporary CVK-STDCERT-20261103 device trustpoint.
The untrusted-CA leaf had the correct IP SAN and valid dates but a fresh unknown
issuer. The hostname leaf chained to the trusted root but its CN/SAN contained
only wrong.example.invalid, while the URL used 192.168.129.111. Key sizes,
algorithms, validity windows, package bytes and HTTPS server configuration were
otherwise compatible. Public roots/leaves and the [PKI manifest](pki-manifest.json)
are retained; ephemeral TLS private keys were removed during cleanup.

An independent [TLS control](independent-tls-controls.json) accepted the valid
certificate and rejected the two deliberately invalid variants with the expected
issuer/hostname errors. Five [offline certificate controls](offline-certificate-controls.json)
also verified that each leaf is valid under its own intended issuer/name and
that the deliberately wrong issuer/name fails. This did not change C9K trust.
The actual C9K valid-certificate control then installed
the package and reached native RUNNING, Pod Ready, HTTP release A and an Available/
Progressing Deployment. This rules out a broken package or generally unusable
certificate setup as the explanation for a negative result.

The native results above come from the actual Standard app-hosting flow, not
from the independent Python client. The [server trace](server-events.jsonl)
separates client 10.1.1.1 (C9K) from the host-side controls. HTTPS servers offered
the same valid package under every certificate variant and did not require
client certificates. Rejection assessment covered the normal three-minute pull
window and subsequent fallback, then required sustained not Ready/no RUNNING,
no package GET/transfer and an observed native TLS rejection. The retained
case events and worker diagnostics correlate those failures with the normal
Standard flow. A download or healthy app under an invalid certificate would be
reported as a failed rejection qualification, not hidden by the harness.

For each case, the outcome was sealed before any repair. Only the endpoint's
certificate was then replaced with the already-qualified valid certificate;
the package URL, payload and Pod template did not change. The existing mutation
fence was left intact. Guard observations retain its actual identity/timing
through normal recovery; no Lease or journal was manually cleared or rewritten.
Both endpoints subsequently passed a valid TLS probe and application health.
The same Pod/app identity was retained in each case through certificate-corrected
recovery.
Normal deletion completed before the next independent case. Case summaries
record whether invalid-certificate health had already occurred, so post-correction
health is not confused with recovery from a rejection.

The candidate matches the earlier
[HTTPS/local control](../standard-iosxe-2026-11-03-https-local/README.md):
source fingerprint `sha256:d07b6ea671fb9508e3eb648014e642eabf845d5fe45d1bd339557cf8b0c098ac`,
binary SHA256 `529e98f251b320567712d7077dcf9b54e5d93737e36100252cf809b2b6db2040`.
Live manager and worker image IDs were checked against that recorded image.
See the [assessment](qualification.json), [raw result](qualification-result.json),
[validation results](automated-checks.json),
per-case invalid outcomes, certificate-correction records and guarded observations.
The production source was unchanged; the prior Go race run was not repeated for
this documentation/evidence-only qualification.

Final cleanup removed the owned fixture/cache files, temporary trusted CA,
three HTTPS listeners and isolated cluster. The original explicit self-signed
HTTPS selector and IRIS client trustpoint were restored. Native signature policy
was preserved; iris remained RUNNING, c9ktest remained DEPLOYED and CAF Stable.
The shared Kubernetes node remained Ready. See [cleanup evidence](final-cleanup.json).

This is a named device/firmware/package/configuration qualification, not a fleet
claim. Expiry, revocation, intermediate-chain variants, redirects, TLS downgrade,
mTLS and registry authentication were not tested. Native package-signature
verification was disabled at baseline and unchanged. No artifact producer
validation, Governed workflow, uninterrupted service, stateful migration or
specific CVE remediation is claimed. The observed TLS rejection and normal
safety-fence recovery do not add an artifact requirement to Standard.

Evidence is allowlisted and sanitized; it excludes Secret data, credentials,
private keys, kubeconfig and unrelated topology/interface observations. Harness
source is under `harness/`. SHA256SUMS covers every exported file.
