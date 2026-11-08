# Pending install lost-response qualification — 3 November 2026

**Passed: accepted-install reply loss with managed worker replacement and
recovery on C9300-24UX / IOS-XE 17.18.1.** Standard remains the default;
Governed remains deferred. One non-blocking status-reporting finding remains.
Lost activate/start replies were not injected in this run.

The tested CVK binary and image are unchanged from the
[2 November core lifecycle qualification](../verified-iosxe-2026-11-02/README.md).
See [candidate provenance](candidate-provenance.json) and the assessed
[qualification result](qualification.json) for exact source/image identities,
UIDs, scope and limitations. Base Git commits alone do not identify the dirty
candidate worktrees.

| Gate | Actual result | Evidence |
| --- | --- | --- |
| Durable intent before install | Protected journal `installPending` observed before the real native request was dispatched | [accepted native response](lost-install-response.json), [request trace](relay-events.jsonl) |
| Genuine lost reply | Native accepted the install and reached DEPLOYED; relay withheld all HTTP response headers/body | [fault event](lost-install-response.json) |
| Independent hardware observation | Direct pinned SSH corroborated native DEPLOYED and exact staged SHA512 outside the relay | [SSH state/checksum](pending-native-ssh-observation.json) |
| Normal managed worker replacement | New worker Pod UID and manager binding, same workload Pod/app/journal identities | [replacement snapshot](snapshot-replacement-still-fenced.json) |
| Conservative fencing retained | Original 1,860-second lease expired naturally; 108 observations confirmed pending/not Ready, DEPLOYED and no replay | [lease](lease-replacement-still-fenced.json), [observations](fenced-observations.jsonl) |
| No install replay | Across both workers: one copy, one config write, one install, then one activate and one start; no rejected duplicate attempts | [complete authenticated forwarding trace](relay-events.jsonl), [result](qualification-result.json) |
| Durable completion and health | Same Pod/app/journal, journal running, native RUNNING, Pod Ready, HTTP 200 release A, Deployment Available and Progressing | [recovered snapshot](snapshot-recovered-a.json), [Deployment availability](deployment-recovered-availability.json) |
| Archive integrity after recovery | Production verifier again confirmed exact size and native SHA512 | [checksum](recovered-native-checksum.json) |
| Normal cleanup | CVK deletion/tombstone and normal lease release preceded worker shutdown; original apps/CAF/trust settings preserved | [deleted snapshot](snapshot-cleanup-native-baseline.json), [cleanup](final-cleanup.json), [host endpoints](final-host-check.json) |

The original mutation Lease and ConfigMap Kubernetes UIDs were retained
throughout. No Lease or journal was manually cleared, replaced or edited to
obtain progress. The complete trace captures `activatePending` and
`startPending` before those subsequent native RPCs as well.

## Timeline and proof boundaries

The real install reply was suppressed at **05:51:04 UTC**. Independent SSH
confirmed the staged archive and DEPLOYED state. The replacement worker's exact
manager binding was observed while the original recovery fence was still held.
Its renewal timestamp stayed **05:50:52.652992 UTC**, with natural expiry at
**06:21:52.652992 UTC**. Activation/start followed after expiry, and the full
Ready/HTTP A result passed at **06:23:03 UTC**. Normal fixture deletion and lab
cleanup completed afterward.

An authenticated lab-only RESTCONF relay recorded requests from the unchanged
worker. It verified the real switch's certificate and presented a short-lived,
explicitly trusted lab certificate to the isolated worker; hostname verification
remained enabled on both connections. A wrong relay hostname was rejected.
Only device HTTPS traffic from the owned nested cluster was redirected. Direct
SSH checksum queries and device-to-store transfer were unchanged. Manual relay
rules stayed in the owned network namespace; Docker managed its temporary,
loopback-only API-port publication normally.

Relay writes were restricted to the one live fixture Pod/app, exact archive
URL/path and app-hosting operations. Scope tests rejected other apps, unrelated
flash paths and platform-wide operations without sending native requests.
See [TLS controls](relay-tls-controls.json), [actual read controls](relay-read-controls.json),
[scope tests](relay-scope-tests.json) and [network scope](relay-network-scope.json).
The relay and routing are qualification tools, not production dependencies.

The forwarding trace establishes exactly one **CVK install request forwarded
to the device**, including across worker replacement. No native installer-internal
counter is exposed. This is stronger evidence of no request replay than an
unchanged RUNNING snapshot, without asserting an internal native installation
counter or an observed runtime rootfs digest.

## Status-reporting finding

The recovered Pod is Running and Ready, and its Deployment is Available and
Progressing, but the Pod's top-level `reason`/`message` retain an earlier
`ProviderFailed` lease warning. See [findings](findings.md) and
[snapshot-recovered-a.json](snapshot-recovered-a.json). This is stale diagnostic
text; native recovery, HTTP A, readiness and availability all passed. No runtime
change was made to hide the finding or change the qualified candidate.

## Retained preparation and observer failures

The relay initially omitted read-only host-meta discovery and an operational
`fields=app` query. These requests were rejected before any native mutation,
with an empty journal. The second rejection retained a conservative preparation
lease. That lease expired normally before the real fault case; no safety state
was reset. Its [original lease](preparation-retained-lease.json) and
[empty-journal snapshot](snapshot-preparation-fenced.json) are retained separately.

The first observer checked the replacement binding before the manager finished
publishing it. The [binding-window snapshot](snapshot-binding-window.json) and
failed observer log are retained. A bounded read-only continuation observed the
converged binding on the same Pod/app/journal/lease. The accepted install and
lost reply were not reinjected, and no Pod binding was manually stamped.
A brief read-only HTTPS readiness refusal after changing the server selector
was followed by successful certificate/hostname-verified controls before the
fixture was dispatched.

## Scope and cleanup

This qualifies the lost **install** reply scenario for this device/firmware,
Linux/amd64 Docker-save fixture and authenticated Copy/checksum profile.
Lost activate/start replies retain automated coverage only. The A fixture does
not establish a particular CVE fix. Single-replica Recreate does not qualify
uninterrupted service, stateful migration, Service routing or fleet compatibility.
Native package-signature verification was disabled at baseline and was preserved.

The fixture was deleted through CVK and its deletion tombstone retained. Only
its exact owned flash archive and `CVK-QUAL-20261103` CA trustpoint were removed.
The baseline explicit self-signed HTTPS selector, existing IRIS client/gNOI
trustpoints and signature policy were restored. Native `iris RUNNING` and
`c9ktest DEPLOYED` and CAF Stable were confirmed. The owned test cluster, relay
and artifact server were removed/stopped; the shared k3s 1.34.5 host node remained
Ready. Private raw captures, isolated-cluster data and original artifacts remain
in the private run directories; repository evidence omits secret material and
unrelated topology observations.

[Automated checks](automated-checks.json), the
[harness manifest](harness/manifest.json) and [SHA256SUMS](SHA256SUMS) identify
retained checks and evidence. Historical 2 November evidence is unchanged.
