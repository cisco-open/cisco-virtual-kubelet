# Standard HTTPS revocation discovery and natural CRL cache refresh

Passed: new leaf revocation was discovered through natural CRL cache expiry and fresh application delivery was rejected.

Qualified profile: C9300-24UX, serial FCW2247C1AJ, IOS-XE 17.18.1 fc2,
IOx 2.13.0. Standard remains the default; Governed is deferred. No production
code changed. The device uses ordinary `revocation-check crl`, with no `none`
fallback, manual refresh, cache flush, cache-policy override or clock change
during qualification. The tested CRL publication interval is a fixture choice,
not a new CVK requirement or a claimed universal discovery deadline.

| Boundary | Measured result (UTC) |
| --- | --- |
| Revocation publication (CRL 4) | 2026-11-04T19:39:59.477841+00:00 |
| Empty cached CRL 3 NextUpdate | 2026-11-04T19:48:19+00:00 |
| First native CRL 4 retrieval | 2026-11-04T19:48:30.261617+00:00 |
| First serial-specific native PKI rejection | 2026-11-04T19:48:30.271000+00:00 |
| First origin-observed TLS alert (copy fallback) | 2026-11-04T19:51:31.254300+00:00 |
| Publication to native retrieval | 510.784 seconds |
| NextUpdate to native retrieval, including next-connection gap | 11.262 seconds |
| Fresh image during cached window | fresh-image-accepted-using-cached-unrevoked-CRL |
| Same-Pod recovery and fresh delivery/deletion | Passed |

The [independent assessment](qualification.json) checks the active signed CRLs and preserved initial list,
exact serial membership, native downloads and cache records, package identities,
actual HTTP release responses, native RUNNING, Pod Ready, managed-worker mode,
Deployment availability and normal deletion. [CRL 3](crl-v3.json) was empty;
[CRL 4](crl-v4.json) added the original server leaf's serial while CRL 3 was still
valid. Both immutable DER/PEM versions are retained. Publication atomically
replaced the bytes at the same HTTP CDP. The TLS certificate/key, root, URL,
device trustpoint and cache policy remained unchanged until the negative outcome
was sealed. Host-side explicit CRL checking rejected the original leaf after
publication, independently of the native device's cached view.

Three different application images distinguish initial valid delivery, delivery
within the old-CRL cache window and delivery after expiry. Each has its own
Docker configuration ID, rootfs layer and exact release. The latter images were
never presented to the C9K before their respective phases. [Build provenance](payload-provenance.json),
source, Docker manifests/configurations, unprimed boundaries and host-only smoke
results are retained. Independent archive hashes are qualification evidence;
Standard received only ordinary HTTPS package URLs with Always, without
appArtifacts, artifact bindings, descriptors, journals or grants.

The [native trace](server-events.jsonl) records package TLS from 10.1.1.1 and
signed-CRL fetches from 192.168.129.2. Read-only native interface observations
bind the latter to Vlan129 on the same pinned C9K. Each fetch records the CRL
number, byte hash and serial membership. Native cache records retain issuer,
LastUpdate, NextUpdate, CDP and DER size. NTP observations confirm current
certificate validity and current published CRLs at every boundary. Separate
TLS contexts and disabled tickets exclude resumed sessions; completed native
sessions were checked for no resumption. No client certificate was required.

The [native PKI diagnostics](native-refresh-diagnostics.json) contain two
serial-specific CERTIFICATE_REVOKED errors: one immediately after CRL 4
retrieval, and one for copy fallback. The origin recorded only the fallback TLS
alert. The raw actor required two origin alerts and therefore retained its
stricter inconclusive classification; the independent assessment combines the
two explicit native PKI errors, matching serial, signed CRL/cache proof,
fallback diagnostic and sustained absence of package delivery/runtime. Neither
the raw trace nor actor result was rewritten, and the fault was not repeated.

The cache-window phase explicitly tests whether publishing CRL 4 is learned while
CRL 3 remains cached. It records the actual outcome rather than assuming immediate
invalidation. Idle observations distinguish a new CRL download without a new
package connection from a fetch triggered by subsequent certificate validation.
After CRL 3 expired naturally, a fresh image deployment exercised native pull and
copy fallback. The negative window retained no Ready/RUNNING fixture or release
and no fresh package GET/transfer when rejection qualified. Timing from
NextUpdate to retrieval includes the gap before the next application connection;
it is not a measurement of a continuously running refresh timer.

After sealing the discovery/rejection evidence, replacing only the served leaf
certificate and matching key recovered the same pending Pod through ordinary
fence expiry. The revoked serial stayed on CRL 4; no root, CRL, device policy,
package, URL, Pod template, Lease or journal was changed to force recovery.
The actual running release was checked again. Fresh native delivery and normal
deletion were then verified. A known release remaining healthy immediately after
publication does not establish automatic revocation-driven eviction, and this
run does not claim it. Software remediation still requires an explicit image
update and verification of the actual running release.

The [initial harness stop](initial-stop-and-continuation.json) is preserved with
its original source and [log](qualification.log). It omitted the workload
namespace and stopped before any C9K handshake or deployment. Creating that
run-owned namespace allowed continuation with the same certificate and initial CRL 1;
the list was not republished to restart its lifetime. The [continuation log](continuation.log)
and exact continuation source remain available. A second prepublication stop followed a successful native deployment: the
server observer encoded the number of an empty CRL as null. The signed DER
hash and native cache record still proved that CRL 1 had been retrieved.
Both attempts and exact original outputs are sealed under
[prepublication-attempt/](prepublication-attempt/PRESERVED.json).
After CRL 1 expired naturally, normal app deletion and new immutable empty
CRL 3 established a new valid cache window; only then was revocation published
in CRL 4. Root, leaf, CDP and device cache policy stayed unchanged. The observer
identified empty CRLs by exact signed-byte hash; raw event records were never
rewritten. [Rearm provenance](prepublication-rearm.json) records this boundary.
No native revocation fault was repeated to conceal an earlier outcome.

[Earlier raw evidence preservation](earlier-evidence-preservation.json) verifies
all captured files from the original revocation matrix and cold-image repeat.
[Static checks](static-checks.json) also verify historical repository evidence
manifests and the unchanged production candidate. The previous 62-package Go
race run applies to that same candidate; it was not rerun for qualification and
documentation work. Documentation and whitespace results are in
[automated checks](automated-checks.json).

[Cleanup](final-cleanup.json) removed the test trustpoint, owned staging archives,
three host fixture images, disposable cluster, origin services and three private
keys. Only after all qualification/recovery/deletion operations and worker stop
did it clear cached CRLs, after proving every remaining entry belonged to this
run. The original explicit self-signed management HTTPS selector and IRIS client
identity selector were restored. iris remained RUNNING, c9ktest DEPLOYED, CAF
Stable, native signature policy unchanged and the shared Kubernetes node Ready.
Internal device image/layer caches were not globally pruned.

Scope is this directly issued leaf, device/firmware and reachable HTTP CDP with
a short initial CRL. It excludes intermediate-certificate discovery, OCSP,
network outages, unavailable/expired replacement CRLs, optional cache-policy
overrides, CA-key compromise, producer-signature revocation, CVE policy and
uninterrupted availability. It does not establish an immediate push mechanism
from a CA to IOS-XE or a fleet-wide maximum discovery time. Ordinary device PKI
configuration is independent of CVK Standard/Verified artifact modes.

Cisco documents default caching until CRL expiry and retrieval on subsequent
validation in its [C9300 PKI guide](https://www.cisco.com/c/en/us/td/docs/switches/lan/catalyst9300/software/release/17-1/configuration_guide/sec/b_171_sec_9300_cg/configuring_authorization_and_revocation_of_certificates_in_a_pki.html).
Measured outcomes above come from this run rather than that general guidance.

Current evidence is allowlisted and sanitized; the prepublication archive
retains exact original bytes and its preservation manifest. Credentials, private keys, kubeconfig,
Kubernetes Secret data and fixture binaries are excluded. Harness sources are
under harness/. SHA256SUMS covers every exported public evidence file.

Repeated sanitized poll and guard snapshots are preserved in the
[indexed archive](observation-snapshots.tar.gz), with an
[uncompressed-content hash index](observation-snapshots-index.json). Boundary
proofs and observation JSONL logs remain directly readable. The prepublication
archive retains its original bytes and separate preservation manifest.
