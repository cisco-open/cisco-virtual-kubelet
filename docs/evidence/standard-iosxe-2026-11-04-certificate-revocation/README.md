# Standard HTTPS certificate revocation — C9K qualification

Configured CRL checking rejected fresh delivery of the revoked leaf and, in the [uncached-image repeat](cold-repeat/README.md), the revoked intermediate. The original warm-image runtime exception is preserved below.

| Certificate / device policy | Native result | TLS rejections in sealed window | Package GETs while revoked | Same Pod retained | Fresh delivery after replacement |
| --- | --- | --- | --- | --- | --- |
| Revoked leaf / none control | Accepted and app ran | 0 | 1 | Yes | Passed |
| Revoked leaf / CRL | Rejected | 2 | 0 | Yes | Passed |
| Revoked intermediate / CRL | Known release A ran; no new transfer | 1 | 0 | Yes | Passed |

Measured from 2026-11-04T10:17:31.882659+00:00 through 2026-11-04T18:03:09.246549+00:00 on C9300-24UX / IOS-XE 17.18.1 fc2,
serial FCW2247C1AJ. Evidence timestamps are UTC. Standard remains the default;
Governed remains deferred. No production code changed.

Revocation checking is an ordinary IOS-XE PKI setting. This qualification does
not add a new CVK Standard requirement or claim that every existing Standard
installation checks revocation. In the original matrix, two run-owned issuer trustpoints used
revocation-check crl with no none fallback; a separate root used explicit
revocation-check none as a control. The [actual policies](pki-policy.json) and
native running-config observations are retained. The disabled-check outcome
is reported separately from the configured CRL profile's rejection result.

The [independent assessment](qualification.json) attributes package TLS to
10.1.1.1 and CRL fetches to 192.168.129.2, the same C9K's Vlan129 interface.
[Native interface, route and cache proof](native-crl-source-proof.json) binds
the latter address to the pinned target. The initial actor's CRL-source counter
recognized only the management address. Its [raw result](qualification-result.json)
is preserved; the assessment recomputes CRL provenance from the full immutable
trace and pinned native evidence. The [attribution note](crl-source-attribution-note.json)
records this observer correction and the executed source hash. Native package
acceptance/rejection observations were unaffected; no fault was repeated or
source address rewritten in the trace. The early invalid-event snapshots are
retained as captured; complete bad-mode transfers before replacement are
assessed from the full append-only trace, because a GET could precede completion
of the transfer and the subsequent health proof.

The [CiscoDevice input](device-input.yaml) omitted appArtifacts. Workloads used
ordinary HTTPS package URLs and Always, with fresh Pods/apps per independent
case and no artifact annotations, descriptors or signatures. Worker
configuration/runtime options and isolated resource inventories confirmed no
artifact journal or grant before, during or after the tests. Normal managed
workers and device mutation safeguards stayed in use.

Three fresh roots isolated the disabled control, revoked leaf and revoked
intermediate cases. All certificate chains, validity windows, IP SANs,
serverAuth usage and signatures were otherwise valid. Five signed CRLs were
published before the first device handshake. They retained the revoked leaf
or intermediate serial from the start; no empty CRL was replaced during the
negative window, so stale pre-revocation caching cannot explain acceptance.
The intermediate control used a different intermediate key/subject, avoiding
an alternate cached valid version of the revoked test intermediate. Only roots
were imported on the switch; peer intermediates were not made trust anchors.

Two actual positive controls reached native RUNNING, Pod Ready, HTTP release A
and Deployment Available/Progressing, then deleted normally. Eleven
[offline controls](offline-certificate-controls.json) first proved valid
name/chain/time without CRL checking and isolated revocation errors at leaf
depth 0 or intermediate depth 1 with checking enabled. Six independent
[host-side TLS controls](independent-tls-controls.json) used explicit CRL-check
flags, rejected revoked fixtures with verification code 23 and accepted the
valid controls. These host results are separate from the actual C9K outcomes.
[Public certificates, signed CRLs and their manifest](pki-manifest.json) retain
DER/SPKI hashes, issuer names, serial membership, reasons, update times and CDPs.

The [server trace](server-events.jsonl) retains native CRL GETs, exact DER
payload hashes and the serials in the served list. Native CRL-cache snapshots
retain the run's issuer, LastUpdate, NextUpdate and CDP. Every case's native NTP
clock observations confirm currently valid certificates and fresh CRLs; no
clock/NTP settings changed. CRL byte hashes and trustpoint revocation policies
were checked unchanged before the negative phase, at its boundary, after
replacement and after normal deletion. CRL caches were not manually flushed
during qualification or recovery.

All original-matrix endpoints offered the identical known-good 5,963,776-byte package
(SHA256 ed8c257297a526229b154cbaabe1c466de239d4c011e4edfd25c7cb440fa071c).
TLS tickets were disabled, contexts were separate per endpoint/mode and native
completed handshakes were checked for no session resumption. Servers did not
require client certificates. Rejection assessment covers the normal native
pull and copy fallback, sustained no package GET/transfer or Ready/RUNNING,
native TLS alerts and observed access to the correct signed CRL. The original intermediate case had one native TLS rejection and zero new
package GETs or transfers, yet its native app ran and HTTP returned release A.
It therefore does not qualify runtime blocking. The raw actor labelled this
inconclusive because it required a GET to classify acceptance; the independent
assessment explicitly records the observed warm-image runtime. The exact
internal IOS-XE cache mechanism was not established.

The separate [fresh-image repeat](cold-repeat/README.md) retained the earlier
matrix intact and used a new binary, Docker config ID, rootfs layer and exact
release `REVOCATION-COLD-20261104-35d8a9c90cb2`. A new independent PKI hierarchy
also excluded a cached valid alternate chain. With CRL checking configured,
the revoked intermediate caused two native TLS rejections, zero package GETs
or transfers and no RUNNING/Ready/HTTP fixture release across 27 observations.
Replacing only the intermediate certificate recovered that exact release in
the original Pod after untouched fence expiry. Fresh native delivery and
normal deletion also passed. All 756 earlier files or append-only prefixes
were [hash-verified unchanged](earlier-evidence-preservation.json).
This qualifies fresh delivery, not cached-content execution blocking.

Leaf repair replaced the served leaf certificate and matching private key,
using a new serial and key. Intermediate repair used a new same-key/subject CA
certificate serial while retaining the server leaf. This intermediate fixture
models a superseded certificate, not CA-key compromise. All old revoked serials
remained on the unchanged CRLs, and all device roots/policies remained unchanged.
No issuer was untrusted/retrusted and no revocation check was disabled to make
recovery pass. Package URL, payload and Pod template stayed unchanged.

Where delivery was rejected, recovery observed the original Pod and canonical
Lease through normal fence expiry. No Lease or journal was cleared, recreated
or manually rewritten. Cases already running before replacement (the none control and warm
intermediate case) are labelled as requiring no rejection recovery; raw actor
flags are retained separately. After replacement health and
normal deletion, every case also deployed a fresh Pod/app with the same URL and
Always: a new native package transfer, RUNNING/Ready/HTTP health, Deployment
availability and deletion prove fresh post-replacement delivery.

The [candidate](candidate.json) matches the earlier
[CA-expiry run](../standard-iosxe-2026-11-03-certificate-chain-expiry/README.md).
Production source and historical evidence hashes were checked. Independent
qualification hashes do not add an artifact requirement to Standard. The prior
full Go race results apply to this unchanged candidate; they were not rerun for
qualification/documentation work. See [validation](automated-checks.json).

[Cleanup](final-cleanup.json) removed only run-owned staging archives, four test
trustpoints, seven HTTPS listeners, two signed-CRL listeners, the cold host image,
isolated cluster and seventeen private keys. Internal IOx/Docker layer caches
were not globally pruned. The documented plural CRL-clear command was unsupported
on this firmware; the [first attempt](cleanup-first-attempt.json) and native-help
discovery are retained. Cleanup resumed with the supported singular
`clear crypto pki crl` command. Any final CRL cache purge occurred only after all
qualification/recovery/deletion operations and the owned worker cluster stop,
and only after proving the entire remaining cache contained run-owned CRLs.
It restored the initially empty cache without touching unrelated cached CRLs.
The original explicit self-signed management HTTPS selector and IRIS client
trustpoint were restored. iris remained RUNNING, c9ktest DEPLOYED, CAF Stable
and the shared Kubernetes node Ready. Native signature policy was disabled at
baseline and preserved.

Results are limited to this firmware/device/package/PKI profile. This does not
measure revocation discovery after later CRL publication, cache-refresh latency,
expired/unavailable CRLs, OCSP, mTLS, redirects or registry authentication.
It does not qualify CA-key-compromise remediation, container-producer signature
revocation, specific CVE remediation or uninterrupted availability. Certificate
replacement can still wait for the ordinary installation fence. Governed stays
deferred; choosing Standard does not require this optional PKI profile.

Evidence is allowlisted and sanitized. It excludes Secret data, credentials,
private keys, kubeconfig and unrelated interface/topology observations. Harness
sources are under harness/; SHA256SUMS covers all exported files, including the
binary signed public CRLs.
