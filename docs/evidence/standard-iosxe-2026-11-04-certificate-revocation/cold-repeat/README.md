# Revoked intermediate with a fresh, uncached application image

**Passed for fresh package delivery on C9300-24UX / IOS-XE 17.18.1 fc2.**
The same Pod recovered after replacement of only the served intermediate
certificate. Standard remains the default; Governed is deferred. No production
code changed. Evidence timestamps are UTC.

| Phase | Native package delivery | Running application release |
| --- | --- | --- |
| Revoked intermediate | Two TLS rejection alerts, zero package GETs or transfers | None; no fixture app RUNNING and Pod not Ready |
| Replacement intermediate, original Pod | Fresh transfer passed after natural fence expiry | `REVOCATION-COLD-20261104-35d8a9c90cb2` |
| Replacement intermediate, fresh Pod | Another native transfer passed | `REVOCATION-COLD-20261104-35d8a9c90cb2` |

See the [before](release-before-replacement.json),
[same-Pod after](release-after-certificate-only-replacement.json), and
[fresh-Pod after](release-after-fresh-deployment.json) release proofs.
The [independent assessment](qualification-assessed.json) checks actual HTTP
release responses, native RUNNING, Pod Ready and Deployment availability, rather
than inferring the installed release from a URL or readiness alone.

The fixture was built specifically for this repeat, with a new static binary,
Docker configuration ID and rootfs layer. It was never presented to the C9K
before the revoked-chain deployment. A host-only loopback smoke test confirmed
its identity; the valid native control served the earlier A image, not this
new image. No global device cache purge or reboot was used. The
[unprimed boundary](unprimed-image-boundary.json), [build provenance](payload-provenance.json),
[Docker configuration](docker-image-config.json), [Docker manifest](docker-manifest.json)
and [fixture source](main.go) are retained.

- Docker configuration ID: `sha256:fb0f4dd6b1012b3e626e42fa64f4fb98bcd5feeba40c34f5c812c4598be2046a`.
- Rootfs layer diff ID: `sha256:dc78be3c3975209b7a2b8d5170dcd7dd6274ec1b72134883d23c9b3265b8f00c`.
- Package SHA256: `624075b6ecc6383fa8615ead9e2bcdb0c410a3f48a3e3351528ef04344b473bd`, 5,959,680 bytes.

The package uses the same Docker-save layout as the earlier working image. The
recorded independent hashes are qualification evidence; they were not supplied
to Standard as artifact metadata or an installation gate.

A new root and independent test intermediate excluded the original run's cached
valid alternate chains. Only the root was imported as a trust anchor, with
ordinary `revocation-check crl` and no `none` fallback. A distinct intermediate
provided a positive native control. Signed CRLs already contained the bad
intermediate serial before first contact. Native issuer/cache observations and
CRL HTTP responses from the pinned C9K's Vlan129 address confirm retrieval of
the correct list. All certificates were otherwise current and correctly named;
NTP was observed without changing the clock. TLS tickets were disabled and
completed native sessions were not resumed.

The negative phase retained 27 observations over five minutes, covering native
pull and copy fallback: no new package bytes, native runtime or HTTP release.
It then retained 103 recovery-guard observations. Certificate replacement
changed only the intermediate serial, preserving its subject/key and the server
leaf. Root, leaf, CRL bytes, revoked serial membership, device policy, package,
URL and Pod stayed unchanged. The replacement represents supersession, not
CA-key-compromise remediation. No CRL cache, Lease or journal was reset during
qualification or recovery.

The original Pod `7e8b7d7a-5dfe-40d4-9576-37e0c0e2e882` recovered after its
normal fence expired at 2026-11-04 17:56:12 UTC. Its HTTP response at 17:57:04 UTC
reported the exact fresh release above. Subsequent normal deletion, fresh
native delivery and normal deletion passed. The canonical mutation Lease kept
the same UID as the original run. Ordinary managed CVK app workers were used
without appArtifacts, bindings, descriptor annotations, journals or grants.

The [original warm-image case](../README.md) remains preserved: known release A
ran despite a rejected TLS handshake and no new package transfer. This cold
repeat qualifies blocking of fresh delivery; it does not turn transport
certificate revocation into eviction or authorization of cached images.
For a vulnerability update, verify the actual newly running release and use
immutable image identities; certificate status alone cannot prove remediation.

All 756 earlier raw evidence files/prefixes were
[hash-verified after the repeat and cleanup](../earlier-evidence-preservation.json).
[Final cleanup](../final-cleanup.json) restored selectors, removed test roots,
owned staging files, host fixture image/services and ephemeral keys, and
preserved unrelated apps and shared Kubernetes health. The initial unsupported
plural CRL-clear command and subsequent native-help discovery are retained;
only after all tests and worker shutdown did the supported singular command
clear exclusively run-owned cached CRLs. Internal IOx/Docker layers were not
globally pruned.

Scope is this device, firmware, fresh fixture and prepublished current-CRL
profile. It does not measure detection of a newly published revocation, cache
refresh latency, unavailable/stale CRLs, OCSP, compromised CA keys or CVE policy.
Public allowlisted evidence omits credentials, private keys and kubeconfig.
The [parent SHA256 manifest](../SHA256SUMS) covers this repeat as well.
