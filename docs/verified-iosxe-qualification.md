# IOS-XE Verified physical qualification

This procedure qualifies application image delivery through the normal managed
CVK app-hosting flow. Standard remains the default; only the selected test device
opts into Verified. Governed is outside this qualification. Worker restart means
replacing the CVK app worker, not reloading or upgrading IOS-XE.

The [2 November run](evidence/verified-iosxe-2026-11-02/README.md) passed the
Copy profile’s deployment, image update, history rollback, running-worker
replacement and device-only same-size corruption rejection on C9300-24UX /
IOS-XE 17.18.1. The [3 November run](evidence/verified-iosxe-2026-11-03/README.md)
then passed lost accepted-install-reply recovery with managed worker replacement,
an untouched lease and one forwarded install request. The
[follow-up reply run](evidence/verified-iosxe-2026-11-03-replies/README.md) passed
independent lost activation and start replies on the corrected candidate. Both
retained the pending intent through normal lease expiry and recovered without
replay. Ready/HTTP health, Deployment availability and cleared top-level Pod
failure text passed. The separate
[Standard control](evidence/standard-iosxe-2026-11-03/README.md) then passed
deployment, managed worker replacement, A → B image replacement, Deployment
history rollback to A and normal deletion on the same corrected candidate.
It omitted appArtifacts and artifact annotations, used ordinary HTTP package
URLs, and created no artifact journal or journal grant. The
[HTTPS/local follow-up](evidence/standard-iosxe-2026-11-03-https-local/README.md)
also passed positive HTTPS delivery, offline preloaded-file installation with
Never, local running-worker replacement and normal deletion. It likewise omitted
artifact metadata and journals. These remain bounded device/firmware/package
qualifications. The separate
[certificate rejection run](evidence/standard-iosxe-2026-11-03-certificate-rejection/README.md)
qualified rejection of an untrusted CA and a mismatched hostname under a trusted
CA through the normal Standard pull and fallback flow. Both blocked package
delivery and readiness; correcting only the certificate allowed normal recovery
after the retained safety fence. No artifact metadata, journal or grant was
required.

The separate [certificate-date run](evidence/standard-iosxe-2026-11-03-certificate-dates/README.md)
qualified expired and not-yet-valid leaf rejection with the same trusted issuer
and correct IP SAN through the actual Standard pull and fallback flow. Device
NTP time was measured without changing clock configuration. Both blocked
package delivery and readiness; replacing only the certificate recovered the
same Pod/app after natural safety-fence expiry. No artifact metadata, journal
or grant was required. These results cover the recorded C9300/IOS-XE profile;
root/intermediate expiry and CRL revocation are covered by the separate runs below.

In the separate [CA-expiry follow-up](evidence/standard-iosxe-2026-11-03-certificate-chain-expiry/README.md),
the C9K rejected both the expired imported root and the expired served
intermediate through native pull and fallback.
The root was imported while valid and expired naturally; NTP time was measured
without changing the clock. The intermediate case kept the root and leaf valid
and used a separate positive-control intermediate to avoid an alternate cached
valid path. Root replacement recreated only the run-owned CA trustpoint, renewed its trust
anchor and served root; intermediate replacement changed the served
intermediate only.
The server leaves, package URLs and payloads stayed unchanged. Post-replacement
health, fresh-Pod native delivery and normal deletion passed in both cases.
Recovery respected normal worker-controlled fences; no Lease or journal was
manually reset.
No artifact metadata or journal was required. Standard remains the default;
Governed remains deferred.

In the [CRL revocation follow-up](evidence/standard-iosxe-2026-11-04-certificate-revocation/README.md),
configured CRL checking rejected a revoked leaf and, in a separate
[uncached-image repeat](evidence/standard-iosxe-2026-11-04-certificate-revocation/cold-repeat/README.md),
a revoked intermediate. The fresh image had a new Docker configuration ID,
rootfs layer and application release. No fixture release ran before certificate
replacement; the same Pod then returned
`REVOCATION-COLD-20261104-35d8a9c90cb2`, and fresh deployment/deletion passed.
Root, leaf, CRLs and policy remained unchanged during intermediate replacement;
normal safety fences were respected. The explicit `revocation-check none`
control accepted the revoked leaf. In the original warm-image intermediate
case, known release A ran with a rejected handshake and zero new package
transfer; that evidence is preserved separately. Transport certificate
revocation alone does not block cached-content execution or prove a software
vulnerability was remediated. Verify the actual running release after updates.
This measured current, prepublished CRLs on the named C9300/IOS-XE profile;
leaf revocation discovery/cache refresh is covered by the separate run below. Ordinary PKI
configuration is optional and independent of CVK artifact mode. No artifact
metadata or journal was required; Standard remains the default and Governed
remains deferred.

The separate [leaf revocation-discovery run](evidence/standard-iosxe-2026-11-04-crl-refresh/README.md)
started with an unrevoked certificate and cached empty CRL, then published its
revocation at the same CDP. A fresh image still ran during the old CRL's valid
cache window. After natural expiry, the next application validation retrieved
the new signed CRL and rejected fresh delivery through native pull and fallback.
Publication to native retrieval was 510.8 seconds; retrieval occurred 11.3
seconds after NextUpdate, including the gap before the next connection. These
are measured times for the fixture's ten-minute initial CRL, not a universal
refresh deadline. Replacing only the leaf certificate/key recovered the same
Pod after normal fence expiry; the actual release, fresh delivery and normal
deletion passed. Two serial-specific native PKI errors establish rejection; the origin recorded
only the fallback alert, and its stricter raw result is preserved. Initial
prepublication harness stops and raw observer evidence are also preserved. No cache policy, clock, Lease or journal was reset. Intermediate
discovery and its failed fresh-execution result are covered below; unavailable
CRLs and cache-policy overrides remain unqualified.
Standard remains the default without artifact metadata or journals; Governed
remains deferred.

The [intermediate revocation-discovery follow-up](evidence/standard-iosxe-2026-11-05-intermediate-crl-refresh/README.md)
observed CRL refresh and a serial-specific native revocation error, but did
**not qualify fresh execution blocking**. A new root/intermediate/leaf hierarchy
and three different image configurations/layers excluded prior valid chains and
preloaded target images. A fresh image ran during the old CRL cache window.
After natural expiry, the C9K fetched the root-issued revocation list 524.2
seconds after publication (9.0 seconds after NextUpdate, including the next
validation gap). No refresh-image GET followed, yet the new Pod became Ready
and its endpoint reported the previous day's leaf-test release. Native RUNNING
predated intermediate replacement; the first HTTP release proof came afterward,
with no intervening package transfer. The internal reuse mechanism is unproven.
The worker treated installation as successful, so copy fallback was not invoked.
Certificate-only replacement did not repair the already-running stale release;
normal deletion and a new Pod under the corrected chain finally delivered and
ran the requested image. All evidence and the failed outcome are retained.
This demonstrates that Pod Ready and TLS revocation do not establish successful
image replacement. No production change, cache prune, Lease/journal reset or
mandatory artifact mode was introduced. Standard remains default and Governed
deferred; unavailable CRLs and cache-policy overrides remain unqualified.

The subsequent [Standard copy-first fix and requalification](evidence/standard-iosxe-2026-11-05-copy-first/README.md)
changes remote delivery to use the existing synchronous RESTCONF copy RPC before
app configuration or installation. CVK requires the qualified completion result,
then installs only from the explicit local path. Failed copies cannot proceed
through native URL installation, unchecked cache filenames or recovery
reconciliation. The default staging file is directly under flash, with no new
directory setup. New Always and IfNotPresent installations require a completed
copy; existing app reconciliation and preloaded local Never remain supported.
The final candidate blocked fresh execution after intermediate revocation
discovery, including across managed worker replacement. Certificate-only repair
recovered the same Pod and exact requested release after the untouched fence;
a fresh IfNotPresent deployment and local Never with its origin offline also
passed, including a local worker restart. The initial missing-parent failure,
its candidate and natural fence expiry are preserved alongside the final run.
An admission-blocked harness restart was preserved and resumed through the
manager; an expired CRL was renewed with the same revoked serial before the
certificate-only recovery phase.
The prior stale-runtime failure report remains unchanged. Standard requires no
artifact metadata or journal; optional Verified still supplies cryptographic
package identity and durable recovery. Existing running apps are not silently
reidentified or repaired by upgrading the worker. Governed remains deferred.

The [copy-first Standard upgrade and rollback qualification](evidence/standard-iosxe-2026-11-06-upgrade-rollback/README.md)
retains the same candidate and verifies A → B → A using a single Deployment and
actual Deployment-history undo. After both images had previously run, with no device image cache pruning, an
HTTPS 404 for B blocked configuration, installation, runtime and Ready. Explicit
history rollback to A respected the original unchanged mutation fence, then
completed a fresh copy and served the exact A release. Restoring B's source
allowed a further upgrade and history rollback. Every successful revision used
a new Pod and a device-local install after confirmed copy completion. No artifact
metadata or journal was needed, and the earlier evidence remains unchanged.
The lab used Recreate with one fixture slot: the old app was removed first, so
failed upgrade and fence recovery caused an availability gap. RollingUpdate,
zero-downtime capacity and automatic vulnerability selection are outside this
run. Retired staging tars remained on flash after native app deletion and were
removed by lab cleanup; automatic flash cleanup was not qualified for that
earlier candidate. Keep versioned upstream packages available for history rollback.
Standard remains default and Governed deferred.

The later [ownership-safe Standard staging cleanup qualification](evidence/standard-iosxe-2026-11-06-staging-cleanup/README.md)
uses a rebuilt candidate and adds automatic cleanup for freshly allocated,
proven-owned temporary tars. Remote A/B upgrades and actual history rollback
passed with each consumed tar confirmed absent while the app ran. A truncated
HTTPS upgrade was blocked before configuration/install and its destination was
confirmed absent; rollback respected the unchanged natural fence. A preloaded
archive with a matching staging-style filename retained its exact size/SHA-512
through all boundaries and passed local Never deployment before and after.
Existing tar inventory and earlier evidence were preserved. The lab did not
manually delete any worker-owned path under test. This firmware left no partial
file after the failed transfer; actual partial-residue deletion is unit-tested.
Preloaded/explicit destinations, legacy files, uncertain operations and files
without a live ownership receipt remain retained. Worker-crash orphan collection
is outside this bounded cleanup contract. Standard still needs no artifact
metadata or journal; Verified is optional and Governed deferred.

A prepared procedure, unit test, historical device result or successful SSH
connection is not a physical qualification pass.

The [1 November attempt](evidence/verified-iosxe-2026-11-01/README.md) found gNOI
File.Get unimplemented on C9300-24UX / IOS-XE 17.18.1. The next run first
established native `verify /sha512` support for an application tar, then reused
the existing RESTCONF copy mechanism. OS.Verify or platform-signature success
alone never establishes integrity of an externally sourced application archive.

## Bind the candidate and target

Record the exact CVK and passthrough source identities, including uncommitted
changes, executable SHA256 values, container image digests and chart/CRD versions.
An image built from a dirty worktree must not be represented by its base Git
commit alone. Record the live manager and worker image IDs and binary provenance
after deployment of the candidate.

Choose one available lab IOS-XE device by Kubernetes context, CiscoDevice
namespace/name/UID, Node name/UID, hardware model and physical serial. Re-read
these identities before every mutation. Historical `.103` lab evidence is not
authorization to assume that device is still idle or running the same software.
Capture IOS-XE version, available flash and app slots, current applications and
their owners, active maintenance operations, worker identities and health.
Stop if current owners or existing mutation claims are unclear.

Use the managed app-hosting readWrite profile. Check the candidate's generated
journal, its ConfigMap UID, exact device annotations, retained Role and named
get/update binding. Confirm verified RESTCONF TLS and the selected delivery
method: Copy needs the existing copy RPC plus pinned SSH file size/SHA512;
GNOI needs authenticated gNOI TLS and File.Put/Get. Confirm native IOx package
signature policy permits the selected test package. Do not bypass TLS or change
native package-signature policy merely to make this qualification pass.

Use a dedicated workload and run label on the selected device. Preserve other
workloads and their configuration. If the device also hosts existing apps,
account for the effect of worker replacement and Node readiness before testing.
Use a one-replica Recreate Deployment for the fixture when app slots do not
permit a surge; that does not qualify uninterrupted service.

## Retain the two test releases

Prepare compatible application releases A and B from explicit upstream digests
and the device's explicit Linux platform. Each must expose an observable release
marker through an independent application probe. Retain the exact package,
descriptor, producer signature, provenance, conversion recipe and checksums.
Record both selected source manifest digests, package digests/sizes, descriptor
digests, signer fingerprint and HTTPS store certificate identity.

Publish both releases under their immutable paths. The worker must trust the
producer key and the store's TLS chain through the supported trust configuration.
Test the actual worker image's trust, not just a workstation CLI using a private
CA option. A and B need not be declared vulnerable/fixed: CVE policy and security
assessment are separate from qualification of exact image replacement.

## Execute the matrix serially

| Case | Action | Required evidence |
| --- | --- | --- |
| Baseline | Capture device, cluster and candidate identity | Exact target binding, no conflicting operation, baseline native app inventory and service probes |
| Initial deployment | Deploy A with its URL and descriptor binding | Correct Pod UID/app ID, exact device file size and the selected qualified checksum (Copy: native SHA512 bound to validated SHA256; GNOI: read-back SHA256), persisted install/activate/start intents, native RUNNING and application probe reporting A |
| Download corruption | Serve different same-size bytes from a dedicated fault-test route while keeping its signed expected identity | Download digest failure, no device transfer or new app configuration/install effect, no Ready result; retained logs/request evidence |
| Device-file corruption | Pause the fixture's worker, stop only the fixture app, corrupt only its retained staged package, then resume the worker | Read-back mismatch blocks start, app remains stopped and Pod not Ready, no new start intent; restore exact bytes and prove recovery to A |
| Running worker replacement | Replace the worker through normal managed reconciliation | New worker Pod UID/admission binding, same workload Pod UID/app ID and journal UID, retained RUNNING record, unchanged native installation count, probe still A |
| Pending-operation replacement | Independently lose accepted install, activate and start replies, replacing the managed worker in each case | Intent persisted before dispatch; authenticated native acceptance and DEPLOYED / ACTIVATED (or STOPPED) / RUNNING observation; same Pod/app/journal retained through untouched lease expiry; no replay; Ready/HTTP health and clean top-level status after guarded recovery |
| Native RUNNING while startPending | Withhold accepted start reply before worker replacement | HTTP may already be healthy; Pod must remain not Ready until guarded journal recovery completes |
| Image update | Change the Deployment template to B's URL and binding together | Old Pod cleanup/tombstone, new Pod UID/app ID and journal record, exact B device checksum/source identity and probe B |
| Rollback | Restore the complete A Pod template through Deployment history/GitOps | Another new Pod UID/app ID, original immutable A descriptor/package reused, fresh verification/installation record and probe A |
| Standard control | Omit appArtifacts and artifact annotations; exercise ordinary URL deployment, worker replacement, image update, history rollback and deletion | Ready/HTTP release at each stage; unchanged app across worker replacement; fresh Pod/app for image replacement; no artifact configuration, journal, journal grant or metadata endpoint; baseline restored |
| Standard HTTPS control | Use an ordinary HTTPS package URL with normal device CA trust | Actual C9K package GET over TLS; native RUNNING, Ready/HTTP health and Deployment availability; no artifact metadata or journal; preserve client identity and restore temporary trust configuration |
| Standard preloaded-file control | Preload an owned package through the existing transfer mechanism, stop its origin, then use the local path with Never | Retained file identity, origin closed before deployment, Ready/HTTP health, same Pod/app across managed worker replacement, unchanged origin trace, normal deletion and owned-file cleanup; no artifact journal |
| Standard certificate rejection | Use fresh unknown-CA/correct-name and trusted-CA/wrong-name certificates with distinct HTTPS URLs and Always; retain normal guards | Positive valid-certificate control; actual C9K TLS failures, no package GET/transfer or Ready/RUNNING while invalid through pull/fallback; certificate-only correction, normal fence recovery and deletion; no artifact journal |
| Standard certificate dates | Use a currently valid root, correct IP SAN, expired/future leaf dates and Always; measure native NTP clock without changing it | Valid control; actual C9K TLS rejection and no package delivery/Ready/RUNNING through pull/fallback; certificate-only repair, same-Pod recovery after untouched fence expiry and normal deletion; no artifact journal |
| Standard CA expiry | Import a short-lived root while valid and let it expire; separately serve an expired intermediate under a valid root/leaf; exclude alternate chains and session resumption | Characterize native rejection or acceptance honestly; renew affected CA certificate only, retain normal guards, prove existing-Pod health and fresh native delivery after replacement; no artifact journal |
| Standard CRL revocation | Prepublish signed current CRLs containing revoked leaf/intermediate serials; configure a run-owned CRL profile and a separate none control; use a genuinely fresh image for the intermediate repeat | Prove native CRL retrieval/cache, TLS rejection and no fresh delivery/runtime before replacement; verify exact running release after certificate-only repair, same-Pod recovery, fresh delivery and deletion; preserve any warm-image runtime exception; no artifact metadata or journal |
| Standard leaf CRL discovery | Cache an empty current CRL while a valid app runs, then publish its revocation at the same CDP; retain default cache policy and use fresh image identities | Measure publication, cache expiry, native new-CRL retrieval and TLS rejection; characterize cached-window acceptance; verify exact release after same-Pod certificate-only recovery and fresh delivery/deletion; preserve earlier attempts; no artifact journal |
| Standard intermediate CRL discovery | Prime a valid chain and empty root-issued CRL; publish intermediate revocation at the same CDP; retain default cache policy and use fresh image identities | Measure signed-list retrieval and serial-specific native error, verify actual runtime even when no GET occurs; preserve stale-image execution and false Ready outcomes; verify normal deletion and corrected-chain fresh delivery; no artifact journal |
| Standard failed-download fix | Use copy before configuration/install, retain old IOx caches, repeat fresh intermediate revocation discovery and replace the managed worker while blocked | Failed copy produces no fixture configuration/runtime/Ready; certificate-only repair recovers same Pod and exact release after untouched fence; fresh IfNotPresent and source-offline local Never/restart pass without artifact metadata or journal |
| Standard copy-first upgrades and rollback | Retain one Deployment, warm A/B images, use real history undo, inject HTTPS 404 for B and keep the original mutation fence | A → B → A exact releases pass; failed B has no configuration/runtime/Ready; rollback waits for natural fence, copies A anew and recovers; source repair, retry B and final A undo pass; Recreate availability gap measured without artifact metadata or journal |
| Standard owned staging cleanup | Use fresh temporary paths, keep a staging-style preloaded canary, upgrade/undo and truncate B delivery | Consumed worker tars automatically absent; failed destination absent before any app install; original fence retained; preloaded size/SHA-512 and local Never preserved before/after; no manual deletion of worker paths or mandatory artifact journal |
| Cleanup | Remove only the run's fixture and temporary fault plumbing | Fixture apps absent, deletion tombstones retained, baseline unrelated apps/health intact, original device mode/config restored if changed for the run |

Every case starts only after the preceding case has a conclusive result. Preserve
failed and blocked attempts; do not overwrite them with a later retry. If native
operation counters or audit records are unavailable, do not infer “no RPC replay”
merely from a RUNNING snapshot or an unchanged final journal phase. Report that
assertion as unproven and retain the narrower observations.

### Bound the fault injections

Download corruption belongs on a dedicated test endpoint and exact fixture
digest route. Never change bytes behind a shared production artifact URL.

For device-file corruption, derive the path from the fixture's journal and
verify that its Pod UID, app ID, package digest and generated `flash:cvk-…` path
all belong to this run. Pause the worker before changing this owned file. Never
write to a system image, another app's package or a guessed filename. Keep exact
original bytes for restoration. A running app's retained staged archive is not
the running filesystem; an already RUNNING record does not continuously hash it.
Stop the fixture app so recovery must verify the archive before issuing start.

Pending-operation testing needs correlated native acceptance and a controlled
lost response, for example a test-only RESTCONF relay restricted to the fixture
app ID. Keep TLS verification and exact upstream device identity. Do not add a
production “skip checksum” or arbitrary fault-injection flag. A restart before
the command reached the device can leave an intentionally unresolved pending
intent; record this outcome rather than deleting the journal to continue.

Worker Pod Ready and manager publication of the workload's exact worker binding
are separate observations. Wait boundedly for the new name/UID binding before
asserting that it converged; never stamp it manually or weaken admission. Keep
failed observer snapshots and resume observation of the same accepted operation
rather than reinjecting the fault to conceal a harness timing failure.

Capture the journal pending record before native dispatch, authenticated native
acceptance and independent native state, the suppressed response boundary, and
the complete request trace across replacement. Record pre-expiry observations
of pending/not Ready, unchanged lease identity/renewal and no additional lifecycle
requests. Let the original fence expire normally before guarded recovery.
A controlled trace can establish one forwarded request per lifecycle operation without claiming
an installer-internal counter that the firmware does not expose.

Preserve canonical mutation Leases and maintenance safeguards during replacement.
An unresolved old operation may correctly block the new worker. Resolve it only
through the existing evidence-based recovery procedure; never manually clear a
Lease or force-delete a journal to obtain a pass.

## Retain and assess evidence

For each case, record UTC start/end, exact commands or controlled test actions,
exit codes, target and candidate identities, relevant Pod/worker/journal UIDs,
source/descriptor/package digests, native state/counters and independent
application probes. Capture journal transitions while they occur; its final
ConfigMap is not a complete transition history.

Use sanitized allowlisted output. Do not retain Secret data, kubeconfig keys,
Authorization headers, producer private keys or raw manifests/logs containing
inline device passwords. Hash retained evidence files and list the hashes in the
run manifest. Keep private raw captures separate from repository evidence.

Qualification succeeds only when every required row is actually executed and
its assertions are supported for the exact candidate and device/IOS-XE version.
Otherwise retain the failed/blocked rows and the access or implementation work
needed to complete them. A pass qualifies this device/software/package profile;
it does not establish fleet-wide compatibility or uninterrupted service.
