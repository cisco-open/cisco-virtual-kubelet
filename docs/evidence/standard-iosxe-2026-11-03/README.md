# Standard app-hosting control — C9K qualification

Passed on 3 November 2026 on C9300-24UX / IOS-XE 17.18.1 fc2, serial
FCW2247C1AJ. Standard remains the default; Governed remains deferred.
No production code change or additional Standard requirement was needed.

| Case | Release | Observations | Result |
| --- | --- | --- | --- |
| initial-a | A | 14 | Ready, HTTP A, Deployment available |
| worker-replacement-a | A | 1 | Ready, HTTP A, Deployment available |
| updated-b | B | 13 | Ready, HTTP B, Deployment available |
| rollback-a | A | 21 | Ready, HTTP A, Deployment available |

The managed [CiscoDevice input](device-input.yaml) omitted `spec.appArtifacts`
entirely. The [Deployment input](deployment-input.yaml) had no Pod-template
annotations, descriptor digest, signature or artifact binding. It used an
ordinary package URL, normal resources and the existing managed-node scheduling
settings. The HTTP fixture origin served only `/standard-agent-a.tar` and
`/standard-agent-b.tar`; it had no descriptor, signature or artifact API.
It supplied the normal external package source, not a new CVK service requirement.

Live controls checked the actual CiscoDevice and generated worker config,
worker arguments/environment names, workload metadata, and ConfigMap/Role/
RoleBinding inventory across the isolated cluster. No appArtifacts configuration,
artifact journal or journal grant was present before deployment, at each health
observation, after worker replacement/update/rollback, or during normal cleanup.
Ordinary worker configuration and existing mutation Leases remained in use;
no Lease was manually cleared or edited.

Initial A reached native RUNNING, Pod Ready, HTTP release A and an Available/
Progressing Deployment. Manager reconciliation replaced the worker through a
CiscoDevice log-level change. The same workload Pod/app survived, its exact
worker binding converged, and the package request count remained one.
Changing the Deployment image to B created a fresh Pod/app and produced HTTP B.
`kubectl rollout undo --to-revision=1`
restored the original A template with another fresh Pod/app and HTTP A.
Old fixture apps were absent at each successful replacement endpoint.

The [origin request trace](store-requests.jsonl) records exactly three successful
GETs from the C9K at 10.1.1.1: A, B, A. The ordinary Standard HTTP URL flow worked;
copy fallback was not separately injected or qualified. Direct RESTCONF TLS was
authenticated.
No relay, injected NAT, artifact CA trustpoint or Verified checksum configuration
was used. SSH was used for independent native observations and lab certificate
selection/restoration, not image transfer.

The candidate is unchanged from the
[lost-reply qualification](../verified-iosxe-2026-11-03-replies/README.md):
source fingerprint `sha256:d07b6ea671fb9508e3eb648014e642eabf845d5fe45d1bd339557cf8b0c098ac`,
binary SHA256 `529e98f251b320567712d7077dcf9b54e5d93737e36100252cf809b2b6db2040`.
Live manager and worker image IDs matched the recorded corrected image.
See [candidate identity](candidate.json), [assessment](qualification.json),
[raw result](qualification-result.json), the `controls-*.json` files and
[worker replacement result](worker-replacement-result.json).
[Validation results](automated-checks.json) record documentation checks and the
unchanged production source; the prior Go race run was not repeated for this
documentation/evidence-only qualification.

Normal scaling to zero removed the final fixture Pod and native app without an
artifact deletion journal or tombstone. Final cleanup removed the owned isolated
cluster and ordinary HTTP source, checked the three owned default cache paths,
and restored the original explicit self-signed HTTPS selector. IRIS client trust,
native signature policy and unrelated apps were preserved: iris RUNNING,
c9ktest DEPLOYED, CAF Stable. The shared Kubernetes node remained Ready.
See [cleanup evidence](final-cleanup.json).

Before any fixture existed, automatic approval review failed or timed out for
SCP staging. After explicit user approval, direct SSH execution of the prepared
script passed automatic review and completed the cases and cleanup. No approval
check was bypassed. Those tooling interruptions were not application failures.

This is an ordinary HTTP package control on one device/firmware/package profile.
HTTPS package delivery and device-local preloaded files were not exercised here.
No lost reply, pending-operation fault, uninterrupted availability, stateful
migration, fleet-wide compatibility, artifact authenticity/checksum gate or
specific CVE remediation is claimed. Native package-signature verification was
disabled at baseline and unchanged. Standard users can opt into Verified
separately when its integrity and durable recovery guarantees are needed.

Evidence is allowlisted and sanitized. It excludes Secret data, credentials,
private keys, kubeconfig and unrelated topology/interface observations. Harness
source is retained under `harness/`. SHA256SUMS covers every evidence file.
