# Standard HTTPS expired root and intermediate — C9K qualification

Both expiry rejection cases qualified.

| CA condition | Native outcome | TLS rejections in sealed window | Package GETs while expired | Same Pod retained | Fresh delivery after replacement |
| --- | --- | --- | --- | --- | --- |
| Expired imported root | Rejected | 2 | 0 | Yes | Passed |
| Expired served intermediate | Rejected | 2 | 0 | Yes | Passed |

Table counts use each sealed negative window. The full trace also retains the
root case's later automatic retries during the documented harness interruption;
the assessment checks that these also delivered no package under the expired CA.

Measured from 2026-11-03T22:52:06.623568+00:00 through 2026-11-04T09:50:01.670453+00:00 on C9300-24UX / IOS-XE 17.18.1 fc2,
serial FCW2247C1AJ. Standard remains the default; Governed remains deferred.
No production code changed. The [assessment](qualification.json) separates
expiry rejection, health under an expired CA, post-replacement health and fresh
post-replacement delivery. A healthy app after replacement does not establish
that expiry was rejected beforehand.

The [CiscoDevice input](device-input.yaml) omitted appArtifacts. All four
Deployment inputs used ordinary HTTPS image URLs and Always, without artifact
annotations or descriptors. Live worker configuration/runtime options and
isolated ConfigMap/Role/RoleBinding inventories confirmed no artifact journal
or grant throughout. Existing mutation safeguards stayed active.

Two unique root CAs were generated. The short-lived root was authenticated on
the device while valid, with its native PEM fingerprint checked. A real app
control downloaded the package, reached native RUNNING, Pod Ready, HTTP release
A and Deployment Available/Progressing, then deleted normally before expiry.
The root subsequently expired naturally; the test waited three additional
minutes and measured the native NTP clock before attempting the negative
transfer. No clock, NTP or time configuration was changed, and no expiry-ignore
exception was configured. The root leaf retained valid dates and the correct
IP SAN for 192.168.129.111. No renewed version of that root was trusted or served
to the C9K before the negative outcome was sealed.

The intermediate case used the other root, valid throughout, and a currently
valid matching leaf. The expired intermediate was sent in the server chain;
it was never imported as a device trust anchor. A separate valid intermediate
and leaf first proved native delivery through this root and a two-level chain.
That control used a distinct intermediate key and subject, so its cached
certificate could not provide an alternate valid path for the actual expired
intermediate. The actual intermediate's renewed same-key/subject certificate
was exposed to the C9K only after sealing the negative outcome. The chains have
no AIA retrieval or alternate cross-signing configuration.

[Public certificates and their manifest](pki-manifest.json) retain exact DER
and SPKI hashes, serials, names and validity bounds. Seven
[offline controls](offline-certificate-controls.json) isolate expiry at CA
depth 1, including successful verification before the old CA's expiry and with
the renewed CA. Independent host-side TLS probes validate the positive setup
and reject each expired-CA fixture with verification code 10 at the actual
negative phase. These are separate from the measured native C9K results.
Nine native-clock records bind certificate validity to NTP time and retain
show clock detail evidence.

The [server trace](server-events.jsonl) distinguishes C9K source 10.1.1.1 from
host-side probes. All endpoints offered the same known-good 5,963,776-byte
package (SHA256 ed8c257297a526229b154cbaabe1c466de239d4c011e4edfd25c7cb440fa071c).
TLS ticket issuance was disabled and contexts were separate per endpoint/mode;
native completed handshakes were checked for no session resumption. Servers
required no client certificate. A rejection pass requires the actual native
pull and copy fallback to fail, sustained no Ready/RUNNING, no package GET or
transfer, and native TLS rejection alerts. Acceptance is recorded as a failure
to qualify rejection, even if the app subsequently runs.

Root repair installs a renewed same-subject/key CA and checks its native PEM
fingerprint, then replaces the served root chain certificate. This firmware
rejected direct re-authentication and explicitly required removing the old CA
trustpoint. Only the run-owned test trustpoint was recreated with identical
settings before importing the renewed CA through crypto pki authenticate. The
leaf, key, package, URL and Pod template remain unchanged. Updating the server's root
alone cannot renew the device's stored trust anchor. This uses Cisco's
[CA authentication procedure](https://www.cisco.com/c/en/us/support/docs/security-vpn/public-key-infrastructure-pki/220422-configure-ca-signed-certificates-with-io.html).
Intermediate repair replaces only the served intermediate with its renewed
same-subject/key certificate; the device root and server leaf stay unchanged.
The [native root replacement trace](root-native-certificate-replacement.json)
and per-case correction records preserve those distinct scopes.

The initial root replacement helper stopped at the firmware's rejected direct
CA-overwrite attempt. The original failure log, helper and
[first native attempt](root-native-certificate-replacement-first-attempt.json)
are preserved. During the extended gap, CVK's normal retries retained the same
Pod and canonical Lease, renewing/reacquiring the fence automatically. The
[continuation note](continuation-note.json) records the old and current renewal
times. Recovery resumed from the sealed root rejection, without repeating the
negative phase or resetting lifecycle state. The corrected root replacement
then respected the current worker-controlled fence; its recorded renewals must
not be confused with manually clearing the original fence. The intermediate
case ran independently after this recovery.

When rejection leaves an installation fence, normal recovery respects it;
observations retain its actual release, renewal and expiry behavior. No Lease
or journal was cleared, recreated or manually rewritten. A case already healthy under an
expired CA is explicitly labelled as requiring no rejection recovery. After
post-replacement health and normal deletion, each case additionally deployed
a fresh Pod/app with the same URL and Always. A new native package transfer,
RUNNING/Ready/HTTP health, Deployment availability and normal deletion proved
fresh delivery after replacement even if expiry had been accepted earlier.

The [candidate](candidate.json) is the unchanged binary/image from the earlier
[leaf-date run](../standard-iosxe-2026-11-03-certificate-dates/README.md).
Production source and historical evidence hashes were checked. Independent
qualification hashes do not add an artifact requirement to Standard. Prior
full Go race results apply to this unchanged candidate; they were not rerun for
this qualification/documentation work. See [validation](automated-checks.json).

[Cleanup](final-cleanup.json) removed only run-owned package caches, two test
trustpoints, four HTTPS listeners, isolated cluster and seven ephemeral private
keys. The original explicit self-signed management HTTPS selector and IRIS
client trustpoint were restored. iris remained RUNNING, c9ktest DEPLOYED, CAF
Stable and the shared Kubernetes node Ready. Native package-signature policy
was disabled at baseline and preserved.

Results are limited to this device, firmware, package and configuration. No
revocation, missing-chain, cross-signing, TLS downgrade, mTLS, redirects or
registry authentication is qualified here. There is no Governed implementation,
producer validation, specific CVE remediation, stateful migration or uninterrupted
availability claim. Certificate replacement may still wait for the ordinary
installation fence; recovery is not promised to be immediate.

Evidence is allowlisted and sanitized. It excludes Secret data, credentials,
private keys, kubeconfig and unrelated interface/topology observations. Harness
sources are retained under harness/; SHA256SUMS covers every exported file.
