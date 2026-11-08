# C9300 application lifecycle qualification — 2 November 2026

Status: **core Verified Copy profile passed** on C9300-24UX, serial
FCW2247C1AJ, IOS-XE 17.18.1. Standard remains the default. Governed is deferred.
This is a bounded physical qualification of the recorded candidate and fixture,
not qualification of every row in the broader procedure.

| Case | Physical result | Evidence |
| --- | --- | --- |
| Initial A | Ready, native RUNNING, HTTP A, exact native checksum | [A snapshot](snapshot-consistent-a.json), [checksum](checksum-consistent-a.json) |
| A → B | A removed/tombstoned; new Pod/app, Ready and HTTP B | [B snapshot](snapshot-consistent-b.json), [checksum](checksum-consistent-b.json) |
| B → original A | Full A template restored with Deployment history revision 2; another new Pod/app, Ready and HTTP A | [rollback snapshot](snapshot-rollback-a.json), [history command](rollback-command.json), [checksum](checksum-rollback-a.json) |
| Running-worker replacement | Manager replaced the worker; workload/app/journal identities retained, Ready and HTTP A, no archive fetch | [worker snapshot](snapshot-worker-replaced-a.json), [result](positive-lifecycle-result.json) |
| Device-only same-size corruption | Worker received valid bytes; switch received altered bytes; native SHA512 rejected them before app configuration/install; journal staging and Pod not Ready | [snapshot](snapshot-transfer-corruption.json), [exact corrupted checksum](transfer-corruption-checksum.json) |
| Wrong checksum / SSH key | Production native verifier rejected each negative control | [checksum rejection](native-checksum-wrong-checksum.json), [host-key rejection](native-checksum-wrong-host-key.json) |
| Cleanup | Existing apps and stable CAF preserved; owned files, services, clusters and temporary trustpoint removed; shared host node Ready | [device cleanup](final-cleanup.json), [host endpoints](final-host-check.json) |

See [qualification.json](qualification.json) for exact identities, limitations,
failed attempts and cases not run. All three release installations independently
matched the native file size/SHA512 and the signed SHA256 package identity.
The [candidate provenance](candidate-provenance.json) binds the dirty source trees,
binaries and image identities; base Git commits alone do not identify these
changes. A second CVK build reproduced the candidate's binary SHA256.

The full Go race suite, managed chart render checks, Kubernetes 1.35 real
API-server smoke suite and 38 passthrough tests passed; see
[automated checks](automated-checks.json). Strict MkDocs and whitespace checks
also passed. The device ran through a dedicated nested k3s 1.35.9 cluster on
ux-agx; its original shared k3s 1.34.5 cluster was not upgraded or restarted.

## Supported checksum method

The device's `verify ?` advertises `/sha512` and `/md5`; it does not advertise
`/sha256`. A native `verify /sha512 flash:alpine_3.18.tar` successfully hashed
an existing **application archive**. The production verifier then checked an
owned newly copied Docker-save tar with its expected exact size and SHA512.
Wrong expected SHA512 and wrong SSH host-key pin were both rejected.

Cisco documents the native SHA512 option in its
[IOS-XE integrity guidance](https://sec.cloudapps.cisco.com/security/center/resources/ios_xe_integrity_assurance.html).
Those documents discuss platform images; applicability to an app tar here is
established by the attached device observations, not inferred from OS.Verify
or embedded platform signatures.

Transfer uses the existing RESTCONF `copy` RPC and an immutable HTTPS URL.
SSH is used only for `dir` and `verify /sha512`, with the approved pinned host
key. No SCP service or container shell is required on the switch. Expected
SHA512 is calculated from the archive after checking its signed SHA256 and
exact size. Descriptor/package identities remain SHA256.

## Test boundaries

Releases A and B are distinct static HTTP fixtures, not a demonstration that a
particular CVE is fixed. Their observable HTTP release markers exercise exact
release replacement. The fixture uses a one-replica Recreate Deployment, so
this does not qualify uninterrupted service or stateful data migration.
The isolated lab cluster and temporary CA are separate from the shared host's
existing cluster. Native apps `iris` and `c9ktest` must remain untouched.
Native IOx package signature verification was disabled at baseline and is not
changed by this qualification. Other package-signature policies require their
own compatible packaging/signing qualification.

## Remaining qualification boundaries

Accepted-operation lost-response recovery was not injected on hardware. Its
durable pending-intent/no-blind-replay assertions have automated coverage only.
A separate physical Standard deployment was not run; Standard defaults and
isolation are covered by the passing regression suite. Native RPC installation
counters were unavailable, so stable app identity and no archive refetch during
worker replacement are not presented as an installation-count measurement.
The fixture requests memory only; general CPU/ephemeral-storage scheduling,
stateful migration, Service routing and fleet compatibility are not qualified.

## Failed attempts and cleanup details

Earlier A instances reached native RUNNING/HTTP A but not Kubernetes Ready;
those were failed deployment attempts. The final candidate fixes read-only
status observation and the mismatch between upstream environment expansion and
informer-based recovery configuration. Read-only preflight rejections release
their own write claim; uncertain mutations remain conservatively fenced.
The retained lease from the failed attempt expired normally before the fresh
sequence. No mutation Lease or journal was manually cleared to obtain a pass.

A direct admin deletion of the reserved worker Pod was denied by admission.
The passing replacement changed CiscoDevice logging from info to warn, letting
the topology manager perform its normal protected rollout. Admission was not
weakened. The checksum-negative run retained its failed staging record and
safety fence; its isolated cluster was stopped before removing only the exact
owned flash files. Private cluster data and original artifacts remain retained
in the private run directory.

The original HTTPS certificate is restored with an **explicit**
`ip http secure-trustpoint TP-self-signed-806566997` selector. This is the one
remaining running-configuration difference: removing the selector now makes
IOS-XE choose the preexisting gNOI trustpoint. No preexisting certificate or
trustpoint was deleted to change that preference. The temporary
`CVK-QUAL-20261102` CA trustpoint is removed; the existing IRIS HTTP-client
trustpoint, gNOI trustpoint and IOx signature policy are preserved. The original
apps remain `iris RUNNING` and `c9ktest DEPLOYED`, with CAF Stable.

The [harness manifest](harness/manifest.json) identifies the retained lab helper
sources. They are evidence for this lab run, not a required user installation
component. [SHA256SUMS](SHA256SUMS) covers the retained evidence files.
