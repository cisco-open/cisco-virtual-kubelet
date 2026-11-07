# Standard HTTPS intermediate revocation discovery and natural CRL refresh

Intermediate revocation discovery was observed, but fresh execution did not qualify: the new Pod became Ready with an earlier release and no new package transfer.

C9300-24UX, serial FCW2247C1AJ, IOS-XE 17.18.1 fc2 / IOx 2.13.0.
Timestamps are UTC. Standard remains the default; Governed is deferred. No
production code changed. This tests ordinary device PKI with `revocation-check
crl`, its default cache policy and a reachable HTTP CDP. No manual CRL refresh,
cache flush, clock change or cache-policy override occurred during qualification.
The initial ten-minute CRL is a test fixture, not a universal refresh deadline.

| Boundary | Measured result (UTC) |
| --- | --- |
| Revocation publication (CRL 2) | 2026-11-05T06:41:12.784144+00:00 |
| Empty cached CRL 1 NextUpdate | 2026-11-05T06:49:48+00:00 |
| First native CRL 2 retrieval | 2026-11-05T06:49:57.020857+00:00 |
| First serial-specific native PKI rejection | 2026-11-05T06:49:57.030000+00:00 |
| Origin TLS alert (copy fallback was not invoked) | Not recorded |
| Publication to native retrieval | 524.237 seconds |
| NextUpdate to native retrieval, including next-connection gap | 9.021 seconds |
| Fresh image during cached window | fresh-image-accepted-using-cached-unrevoked-CRL |
| Certificate-only recovery in original Pod | Expected release not recovered |
| Normal deletion and fresh delivery under replacement chain | Passed |

The C9K fetched the new CRL and logged the original intermediate serial as
revoked. Nevertheless, the newly requested app reached native RUNNING and Pod
Ready without any refresh-image GET. Its endpoint returned the previous leaf
qualification release `CRL-REFRESH-REFRESH-20261104-a674b004ffa2`, instead of
`INT-REFRESH-REFRESH-20261105-4cd216c69bce`. See
[confirmed unexpected runtime](unexpected-runtime-confirmed.json) and
[its native/Pod/HTTP snapshot](snapshot-unexpected-confirmed-runtime.json).
Native RUNNING began before certificate replacement; the first confirmed HTTP
release was observed afterward, with zero intervening package transfers.
The exact internal reuse mechanism was not established. No cache was pruned to
force a rejection. Copy fallback was not invoked: the worker considered the
native installation successful. This does not qualify a fresh-execution gate.

A new independent root and intermediate hierarchy excludes previously cached
valid alternate chains. Only the root was imported as a trust anchor. The
original intermediate and server leaf were valid and unrevoked at first native
contact. The leaf carries a CDP for the intermediate-issued empty CRL; the
intermediate carries a CDP for the root-issued CRL. [CRL 1](crl-v1.json) initially
contained no revoked intermediate. After actual valid-chain deployment,
[CRL 2](crl-v2.json) added the original intermediate's serial at the same URL,
while CRL 1 was still current. The origin certificate chain, root, leaf, URL and
device policy stayed unchanged during publication and the negative window.

The [independent assessment](qualification.json) verifies signatures, issuer
identity, exact serial membership, native retrieval hashes, cache timestamps,
certificate validity under observed NTP, and actual application releases.
[Offline controls before publication](offline-controls-v1.json) and
[after publication](offline-controls-v2.json) isolate revocation at depth 1;
ordinary chain/name/date validity remains intact. Host-side CRL-checking TLS
controls are recorded separately from the C9K's native outcomes.

Each phase uses a freshly built image with a different Docker configuration ID,
rootfs layer and compiled release. The initial valid, cached-window and
post-expiry images were not preloaded on the C9K. Host-only loopback smoke tests,
[build provenance](payload-provenance.json), fixture source, Docker manifests and
configurations are retained. The [cached-window](cached-image-unprimed.json) and
[post-expiry](refresh-image-unprimed.json) boundaries prove no earlier native
package transfer of those images. Archive hashes are qualification evidence;
Standard received ordinary HTTPS URLs and Always without artifact metadata,
descriptors, bindings, journals or grants.

The cached-window deployment measures whether the newly published revocation is
known before the old CRL expires. Idle observations retain any autonomous fetch;
the post-expiry deployment triggers a new validation using a previously unseen
image. The [origin trace](server-events.jsonl) records both signed CRL payloads,
serial membership, source addresses and package transfers. Package TLS comes
from 10.1.1.1; CRL fetches come from the same pinned C9K's Vlan129 address,
192.168.129.2. Native interface and cache observations bind those addresses and
retain LastUpdate, NextUpdate, CDP and DER size. Source hashes and actual signed
CRL numbers are retained even for empty lists. Completed native TLS sessions
were checked for no resumption; tickets were disabled.

[Native PKI diagnostics](native-refresh-diagnostics.json) retain only relevant
revocation errors for the original intermediate serial. They complement origin
alerts: a server-side established TLS session alone does not prove client
certificate acceptance. Native pull, copy fallback, no fresh package delivery,
Pod readiness, native state and actual HTTP release are assessed together. The
raw actor result and independent assessment are both retained. If delivery or
runtime succeeded under revocation, that outcome remains explicit rather than
being concealed by later recovery.

After sealing the negative phase, only the served intermediate certificate was
replaced: same subject and public key, new unrevoked serial. The server leaf and
key, root, empty intermediate-issued CRL, root-issued CRL 2, URLs and packages
remained unchanged; the old serial stayed revoked. No issuer was reimported and
no trust/cache policy was disabled. This models certificate supersession, not
CA-key-compromise remediation. The replacement was never served to the C9K
before the negative boundary, so it could not provide an alternate cached valid
chain. Host/offline checks of it did not contact the C9K.

Replacing only the intermediate certificate did not change the already-running
stale release. The original actor's unsuccessful expected-release wait was
stopped, its log hash and all observations retained, and the same Pod's wrong
release recorded. Normal deletion followed by a new Pod under the replacement
chain then proved a real package transfer and the exact requested release,
plus native RUNNING, Ready, Deployment availability and normal deletion.
No Lease/journal reset, clock change or manual CRL refresh occurred.
This failed execution result demonstrates why software remediation requires an
actual running-release check: Pod Ready and transport revocation alone do not
prove that the requested image ran. Standard remains default; this qualification
does not introduce a mandatory artifact mode or implement Governed.

[Earlier evidence preservation](earlier-evidence-preservation.json) verifies
original revocation, cold-image and leaf-discovery records. Historical repository
manifests and the unchanged production fingerprint are in
[static checks](static-checks.json). The previous 62-package Go race run applies
to the unchanged candidate; it was not rerun for this qualification/documentation
work. See [automated checks](automated-checks.json).

[Cleanup](final-cleanup.json) restores the original explicit self-signed
management HTTPS selector and IRIS client identity selector. It removes the
owned trustpoint, staging files, origin services/images, disposable cluster and
three ephemeral origin keys. Cached CRLs are cleared only after normal workload
deletion and worker shutdown, and only if every cache entry is proven run-owned.
Unrelated apps, CAF health, native signature policy and shared Kubernetes health
are preserved. Internal device Docker/image caches are not globally pruned.

Scope is this intermediate hierarchy, firmware, fresh-image fixtures and current
reachable CRLs. It excludes OCSP, missing/expired replacement CRLs, network
outages, cache-policy overrides, CA-key compromise, producer-signature revocation,
CVE policy and uninterrupted availability. Time from NextUpdate to retrieval
includes the gap before the next validation; it is not an autonomous timer bound.
Ordinary device PKI is independent of CVK Standard/Verified artifact modes.

Cisco describes default caching and subsequent CRL retrieval in the
[C9300 PKI guide](https://www.cisco.com/c/en/us/td/docs/switches/lan/catalyst9300/software/release/17-9/configuration_guide/sec/b_179_sec_9300_cg/configuring_authorization_and_revocation_of_certificates_in_a_pki.html).
The measured results here come from the physical run. Earlier leaf-only results
remain in their [separate immutable report](../standard-iosxe-2026-11-04-crl-refresh/README.md).

Evidence excludes credentials, private keys, Kubernetes Secret data, kubeconfig
and fixture binaries. Repeated sanitized poll/guard snapshots are preserved in an
[indexed compressed archive](observation-snapshots.tar.gz), with
[uncompressed-content hashes](observation-snapshots-index.json). Boundary proofs
and observation JSONL remain directly readable; SHA256SUMS covers every export.
