# Verified artifacts and device delivery

Standard remains the default. Existing HTTP(S) package URLs and device-local
files need no new annotations, public keys, services or resources. The optional
Verified implementation currently provides immutable package preparation in
docker-http-passthrough, signed metadata preflight and IOS-XE delivery in CVK.

Verified device installation is available experimentally for IOS-XE workers with
verified RESTCONF TLS, an explicitly configured staging/checksum method, and a
protected persistent installation journal. Standard remains the default. NX-OS and drivers without
this runtime reject Verified execution. Governed remains unimplemented.

The optional Copy profile passed initial deployment, A → B → original A
replacement, running-worker replacement and same-size device-transfer corruption
rejection on C9300-24UX / IOS-XE 17.18.1 in the
[2 November core run](evidence/verified-iosxe-2026-11-02/README.md).
The [3 November run](evidence/verified-iosxe-2026-11-03/README.md) additionally
qualified a lost accepted-install reply: the replacement worker retained the
same Pod/app/journal, respected the original 31-minute fence and resumed
activate/start without another install request. No runtime change was needed.
The [follow-up reply run](evidence/verified-iosxe-2026-11-03-replies/README.md)
qualified independent lost activation and start replies on a candidate that fixes
the earlier stale Pod diagnostic text. Each pending intent survived managed
worker replacement and normal lease expiry, then recovered without replay.
Native RUNNING/HTTP A did not bypass startPending readiness gating. Both cases
finished Ready with healthy HTTP, Available/Progressing Deployments and empty
Pod reason/message. The historical
[finding](evidence/verified-iosxe-2026-11-03/findings.md) remains preserved.
See the [qualification procedure](verified-iosxe-qualification.md) for exact
scope and remaining gates. This is a bounded experimental device/package profile.
The earlier [gNOI attempt](evidence/verified-iosxe-2026-11-01/README.md) found
File.Get unimplemented despite successful OS.Verify. Copy uses the existing
transfer RPC and a separately established native file checksum method instead.
Managed shared app-hosting workers receive journal provisioning and bounded
RBAC grants when Verified is selected.

The separate [Standard control](evidence/standard-iosxe-2026-11-03/README.md)
passed ordinary HTTP package deployment, managed worker replacement, A → B image
update, Deployment-history rollback to A and normal deletion on the corrected
candidate. The CiscoDevice omitted appArtifacts; the workload omitted artifact
annotations. Live worker configuration and resource inventories confirmed no
artifact journal or journal grant. Standard therefore retains the existing URL
flow without adopting Verified's metadata or journal requirements. The separate
[HTTPS/local follow-up](evidence/standard-iosxe-2026-11-03-https-local/README.md)
passed positive HTTPS delivery with normal origin CA configuration and a
preloaded flash image with Never while the package origin was stopped. The local
app also survived managed worker replacement with no package request. Both
controls omitted appArtifacts and artifact annotations and created no artifact
journal or grant. The separate
[certificate rejection run](evidence/standard-iosxe-2026-11-03-certificate-rejection/README.md)
qualified rejection of an untrusted CA and a mismatched hostname under a trusted
CA through the normal Standard pull and fallback flow. Both blocked package
delivery and readiness; correcting only the certificate allowed normal recovery
after the retained safety fence. No artifact metadata, journal or grant was
required. Independent qualification checksums do not add a required Standard
runtime gate.

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

## Prepare and retain a package

In the separate docker-http-passthrough project, use its `prepare` command with
an immutable source digest and explicit platform:

```sh
docker-http-passthrough prepare \
  --registry https://registry.example.com \
  --repository vendor/agent \
  --digest 'sha256:<source-manifest-or-index-hex>' \
  --platform linux/amd64 \
  --output-dir ./prepared-agent
```

Replace example placeholders with real values. The destination must not exist.
The command verifies upstream content digests, sizes, platform and uncompressed
layer identities, then publishes the local output directory atomically. It
produces `package.tar`, `descriptor.json`, `provenance.json`, `recipe.json` and
`checksums.json`. No-argument server operation remains Standard. See the
[passthrough README](https://github.com/cisco-open/docker-http-passthrough#verified-preparation-command)
for supported registries, authentication, redirect allowlists and resource bounds.

Preparation does not sign, approve, scan or install the artifact. Its output is
unsigned. A trusted producer pipeline signs the exact descriptor bytes, including
the final newline, and retains the package and associated evidence. Do not edit
or reserialize a descriptor after signing. Supplier signature checks and CVE
scans can be added by that pipeline according to its own policy; neither requires
deploying Governed infrastructure.

## Signature and storage contract

The initial verifier accepts Sigstore v0.3 **message-signature** JSON bundles
with an operator-pinned PKIX `PUBLIC KEY` PEM. The signature hash follows
Sigstore's default algorithm for that key (SHA-256/384/512, or raw Ed25519);
descriptor and package identities always use SHA-256.
Supported keys are RSA 2048/3072/4096, ECDSA P-256/P-384/P-521 and Ed25519. Use a
signer that produces this bundle format, such as Cosign with a producer key.
Legacy Cosign bundle JSON, keyless certificate identities and DSSE envelopes are
not supported. Transparency-log entries and timestamps do not add assurance in
this profile; the verifier authenticates against configured keys directly. Keep
private keys in the producer pipeline, never on CVK or in Pod annotations.

Publish unchanged bytes under an HTTPS origin with these exact paths:

| Content | Path |
| --- | --- |
| Descriptor | `/v1/descriptors/sha256/<descriptor-hex>.json` |
| Descriptor signature bundle | `/v1/descriptors/sha256/<descriptor-hex>.sigstore.json` |
| Archive | `/v1/packages/sha256/<package-hex>/package.tar` |

Digest names identify SHA-256 over the exact stored bytes. Preserve immutable
objects and previous versions. CVK metadata requests use certificate validation,
no redirects and no embedded URL credentials. Descriptor and signature limits
are 256 KiB and 1 MiB. Declared packages default to a 4 GiB maximum; an operator
can set a limit up to 64 GiB. The CLI uses system TLS roots by default;
`--ca-file ./store-ca.pem` supplies a private CA bundle instead. It does not yet
support authenticated metadata stores. Use a private network and trusted server
certificates where needed; do not disable TLS verification.

## Run CVK preflight

After publishing the descriptor and signature bundle:

```sh
cisco-vk artifact verify \
  --store https://artifacts.example.com \
  --producer-key ./producer.pub \
  --platform linux/amd64 \
  --descriptor 'sha256:<descriptor-hex>' \
  --package-url 'https://artifacts.example.com/v1/packages/sha256/<package-hex>/package.tar' \
  --package-file ./prepared-agent/package.tar
```

The command rejects an unexpected descriptor digest, duplicate or unknown JSON
fields, untrusted/invalid signature, wrong platform, wrong package URL or
oversized content. Repeating `--producer-key` supports up to eight pinned keys
during rotation. With `--package-file`, it also streams and checks that local
file's exact size and SHA-256. Without it, the result covers signed metadata
only; it does not fetch the package from the store.

Success prints JSON with `verificationScope`, descriptor, manifest, package and
producer-key digests. `deviceInstallationVerified` is always `false`. Success
does not establish device compatibility, application health, vulnerability
status or deployment authorization. The local package must still be checked
again on the device before a future Verified installation.

The library also validates the planned per-container annotation:

```yaml
metadata:
  annotations:
    cisco.io/apphost-artifacts: >-
      {"agent":{"descriptorDigest":"sha256:<descriptor-hex>"}}
```

Bindings must match every regular container by name. `releaseID`, policy grants
and other Governed fields are rejected by the Verified parser. Annotation
parsing is enforced by the IOS-XE Verified deployment path. Standard does not
require or claim verification from this annotation.

## Enable IOS-XE Verified delivery

Select `appArtifacts.mode: Verified` with the store URL, pinned producer keys,
explicit source platform and optional package limit already described. Configure
RESTCONF TLS with certificate verification enabled. No insecure
transport override is accepted. Native IOx package signature policy remains a
separate device requirement; CVK does not disable it for Verified.

Two delivery methods are available only within the optional Verified mode:

- Omitted `delivery`, or `delivery: GNOI`, preserves the experimental gNOI
  File.Put/Get path. It additionally requires authenticated gNOI TLS.
- `delivery: Copy` reuses the existing RESTCONF copy RPC to fetch the exact
  immutable HTTPS package URL into a Pod/digest-specific flash file. Native
  `dir` and `verify /sha512` queries run over SSH port 22 using the device's
  existing username/password and a required `sshHostKeySHA256` fingerprint.
  This adds no SCP service or application-side shell. The device HTTPS client
  must support the store's TLS/cipher/trust configuration.

For example, add these fields alongside the existing Verified trust settings:

```yaml
appArtifacts:
  mode: Verified
  delivery: Copy
  sshHostKeySHA256: SHA256:<approved-device-host-key-fingerprint>
  # storeURL, producerPublicKeys and platform are also required.
```

Copy first downloads and checks the exact archive's SHA256 and size in the
worker, then calculates SHA512 from those validated bytes. The native staged-file
size and SHA512 must match before configuration/install/activate/start. SHA512
therefore remains bound to the signed SHA256 artifact identity without changing
the contract or accepting a device-reported hash as the expected value. A
replacement worker recomputes this binding from the retained archive whenever
recovery needs verification. Healthy RUNNING records do not redownload archives.

Transfer and checksum failures retain the journal and block installation;
unsupported algorithms, malformed output and mismatched host keys never trigger
an automatic transport or Standard fallback. Native copy is bounded by the
operation deadline, but cannot enforce the worker's byte limit while receiving
on the switch. Use a trusted immutable store and monitor flash capacity; a
checksum mismatch protects execution but cannot undo space consumed by a bad
transfer. The worker-side download retains its exact byte limit.

### Desired Pod configuration

Verified keeps the original Pod configuration in lifecycle callbacks and
informer-based recovery. It disables upstream Virtual Kubelet environment
expansion only for this mode, because injected service variables and unordered
map expansion change the configuration hash across callbacks or worker restarts.
Explicit `env` values and the driver's supported Secret/ConfigMap key references
remain available. `envFrom`, field/resource references and automatic service
environment injection are not supported in this initial Verified profile; use
explicit entries. Unsupported references fail before device mutation. This does
not change Standard's environment handling.

An immutable artifact or effective device configuration change requires a new
Pod UID. Update the complete Deployment template (archive URL and descriptor
binding together); roll back the complete retained template. A rejected desired
configuration update is a read-only preflight outcome and cannot release an
unrelated uncertain write. Healthy status reads do not acquire a mutation Lease.
Pending lifecycle records and stopped-app recovery still require guarded writes.

### Managed shared app-hosting workers

Upgrade the controller/chart with this implementation, then opt the CiscoDevice
into Verified using `spec.appArtifacts`. Use the normal managed app-hosting
**readWrite** profile. The controller provisions these objects in the device
namespace before launching its worker:

| Object | Contract |
| --- | --- |
| ConfigMap | One durable journal per CiscoDevice UID, with exact device namespace/name/UID annotations |
| Role | `get` and `update` on that single ConfigMap using `resourceNames` |
| RoleBinding | That Role bound only to the namespace's shared app-hosting ServiceAccount |

The object name is `cvk-artifacts-` followed by the first 24 hex digits of SHA256
over the CiscoDevice UID. The name is stable across worker restarts and address
changes; recreating the CiscoDevice gives a new incarnation and journal. The
worker validates the manager's journal identity and pins the ConfigMap's own
Kubernetes UID, so replacing the ConfigMap cannot silently reset a running
worker's history.

Standard devices create no artifact journals or grants. Read-only and disabled
app-hosting profiles receive no journal write grants. The fixed shared worker
ClusterRole contracts are unchanged. The manager additionally needs Role `create`
authority (included in the regenerated chart); it already has the ConfigMap
permissions necessary for this bounded delegation. It receives no RBAC `escalate`
or wildcard grant for this feature.

The shared app-hosting identity has the union of these named grants for Verified
devices in its namespace. RBAC does not distinguish individual workers using
that same ServiceAccount. Existing device-bound worker admission and mutation
Leases still fence physical device operations. Existing privileged network
worker ConfigMap permissions also remain unchanged: controller identities in the
protected device namespace remain trusted. This integration does not establish
per-device isolation between those privileged controller identities.

The namespace audit rejects ordinary namespace delegations that can create,
modify or delete active or retained journal objects. Exact controller-managed
journal grants are validated against their live Role, binding and journal through
the uncached reader. Additional subjects, broader rules, wrong device identity
or unexpected additive grants trigger the existing shared-worker quarantine.
Role/RoleBinding changes and journal metadata/deletion events rerun the audits;
ordinary journal data writes do not trigger namespace-wide reconciliation.

Switching back to Standard, dropping the app-hosting write profile, or completing
device deletion/handoff removes the relevant write bindings. Peer Verified device
grants remain. A deleting device's grant is retained while its worker is still
active, so it can persist cleanup state; removal follows worker shutdown. Journals
and their unbound Roles have no device owner reference and are retained rather
than garbage collected. A retained Role with a missing journal is a loss of
installation evidence: the controller fails closed instead of recreating an
empty journal. Foreign objects at reserved names are never adopted.

### Standalone workers

Standalone workers continue to use the existing address-derived journal name.
Create an empty ConfigMap in the protected device/operation namespace before
starting the worker. Its name is `cvk-artifacts-` followed by the first 16 hex
digits of SHA256 over the exact configured device address. For example:

```sh
DEVICE_ADDRESS=192.0.2.10
JOURNAL=cvk-artifacts-$(printf '%s' "$DEVICE_ADDRESS" | shasum -a 256 | cut -c 1-16)
kubectl -n device-system create configmap "$JOURNAL"
```

Grant that operator-managed identity `get` and `update` on the named ConfigMap
with a namespaced Role/RoleBinding. Keep the existing device maintenance Lease
permissions and use one canonical device name/namespace across all workers and
software operations. Do not give workload users journal write access.

### Installation and recovery

Verified automatically enables the existing maintenance write fence, without
enabling reboot or other operational actions. Default Standard deployments add
none of these requirements or grants.

The worker refuses startup if it cannot read the journal. Before each device
mutation it must persist its intent. A journal write failure prevents the next
mutation. The journal is not an emptyDir or a worker-local file; retain and back
it up across worker replacement. Do not delete or edit records to retry a failed
installation. A missing record with an existing application is rejected.

Set each container image to its immutable package URL and provide the matching
`cisco.io/apphost-artifacts` binding above. The driver resolves and verifies all
container descriptors before mutation, downloads exact package bytes to a bounded
temporary file, then stages and verifies a Pod/digest-specific flash path using
the selected delivery method. GNOI streams File.Get through SHA256 and an exact
size bound; both streamed and terminal device SHA256 must match the descriptor.
Copy uses the SHA256-to-SHA512 binding and native file verification above.
Installation uses only that local flash path and disables automatic start in the
application configuration. Explicit install, activate and start each have a
persisted intent and an independent device observation before advancing.

After a worker restart or lost RPC acknowledgement, pending operations are
observed rather than resubmitted. Matching DEPLOYED, ACTIVATED/STOPPED or RUNNING states
complete the corresponding recorded step. A previously completed app that stops
or deactivates is restarted through fresh checksum checks and durable intents,
without reinstalling. An operation that never took effect
can remain pending; CVK fails closed instead of guessing that a repeat is safe.
Checksum mismatches, missing staged files, changed descriptor/configuration or
an application without an installation record block readiness. Deletion records
a retained tombstone before cleanup, preventing stale callbacks from reinstalling
the same Pod. Use a replacement Pod with a new UID for a fresh installation.

Healthy status observation validates the desired binding, complete installation
identity, retained RUNNING journal and live RUNNING state without a device write
Lease, package download or lifecycle RPC. An in-progress installation returns
Pending rather than blocking a status read behind its runtime lock. Pending
journal phases and stopped/deactivated apps still enter guarded recovery. A
native RUNNING snapshot alone cannot complete an installPending record.

Read-only preflight rejection is distinguished internally from an attempted
device mutation, including across virtual-kubelet's legacy error wrapper. It
does not retain a mutation fence. Errors after entering installation/recovery,
ambiguous RPC outcomes and cancellation of possible device work retain the
conservative fence (up to the existing 31-minute Lease). Do not edit or delete
that Lease to force a retry. Qualification reports must preserve any such
blocked attempts instead of counting a manual reset as recovery success.

Readiness here means observed RUNNING after the verified installation sequence;
it does not add application health probes. This profile assumes exclusive CVK
ownership of its generated app IDs and a trusted device administrator. It cannot
detect an external administrator replacing a running app under the same ID.
After a device reset or external changes, replace the Pod rather than treating
an old journal as new installation evidence.

Temporary host files are removed after transfer. Staged device files and journal
records are retained for recovery; operators must monitor device capacity and
journal size. A ConfigMap has the Kubernetes object size limit, so retire records
only after the corresponding Pods and apps are permanently removed and stale
callbacks cannot return. Automatic garbage collection is deferred.

For a security update, prepare and sign a new source digest and package, then
change both the image URL and descriptor binding in the Deployment Pod template.
The new Pod UID obtains a new installation record and staged path. Keep previous
immutable artifacts for rollback through another normal Pod replacement. Scans
may drive this pipeline; automated vulnerability policy, expiring grants and
fleet campaign controls remain future Governed work.
