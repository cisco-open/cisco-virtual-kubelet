# Public device CA and policy migration qualification

This follow-up implements part of R3 and exercises R1 migration. It is **not**
full-roadmap acceptance. The immutable 71-gate [checkpoint](README.md) remains
unchanged; a new implementation or green CI run cannot close unrelated gates.

## Implementation and scope

`83e7e10a7aacb665a150c2e200a1a998e5fd5781` adds opt-in
`CiscoDevice.spec.tls.caSecretRef`. The controller resolves a same-namespace
public `ca.crt` into both per-device workers, without exposing gNOI signer keys.
Material invalid at inspection is replaced by an empty mount. The initial
direct Secret projection is superseded by the snapshot hardening below; its
validation alone cannot prevent kubelet refreshing later source changes. Startup checks
the manager-bound SHA-256 against the mounted bytes before creating clients.
Managed software claims re-read the Secret revision; preparation trust binds it.
Existing deployments without the reference retain their prior TLS behavior.

The native Deployment policy permits only the fixed public-CA key, or an empty
mount while invalid. Its exact contract digest changes. An older manager fails
startup rather than dropping the new API field. The new worker startup flag
also rejects older binaries. This does not change the two functional worker
accounts, their existing Secret-read permissions, or provision device keys.
See the [configuration contract](../../CONFIGURATION.md#shared-tls).

Local qualification of that source includes the full race suite, public/private
bundle negative cases, real-API schema/defaulting, Secret rotation/removal,
runtime byte mismatch and current-revision claim checks. The disposable native
Kubernetes 1.35 suite passed including actual October, earlier lab and immediately
preceding manager rejection, incomplete-policy rejection, candidate recovery and
restart. The native public-key projection probes passed. The test binary used
the working tree subsequently committed as `83e7e10a`; the startup helper's
printed parent HEAD is not the source identity of those uncommitted test bytes.
The full race suite was separately rerun after that commit.

## Physical migration regression and repair

Ubuntu16 Helm revision 163 installed the matching chart/admission contract and
`83e7e10a` manager. The additive CiscoDevice CRD applied server-side without
forced field ownership. Planned network account rotation correctly waited on a
historical `.101` leaf:

```text
roadmap-e09-transfer-verify-20261002-101-r2-cat9k-lab-101-d79c3c38
phase: <empty>
managerAdmission: Settled / rollout-v1
managerControl: cancel=true, revision=1
worker status, claims, receipts: absent
```

Its original UID is `13794841-edd7-4809-8fda-6fd96f1a88be`. It predates the
staged protocol, and could never be granted again. The queue/quiescence helper
incorrectly required the newer PrepareOnly protocol even for this entirely
empty cancellation record. No claim, Lease, receipt or finalizer was cleared.

`dbdbb4e7cbc4ce258fa3275b620ac01a2c409556` fixes the common idle-authority predicate.
The compatibility exception requires exactly the old, unpaced PrepareOnly
shape, settled identity-bound admission, cancellation without pause, and **no
worker status at all**. It does not change live protocol eligibility. Negative
tests include claims, receipts, dispatch markers, any worker field, identity or
control mismatch, unknown protocol, network requirements and preinstalled use.
The actual controller access-transition test additionally proves retained audit
is byte-for-byte unchanged and a claimed operation remains blocked.

The regression failed before the fix and passes after it. The full software
lifecycle/controller race suites pass. The final candidate's full race suite
also passes (61 packages, including cached unaffected packages).

The exact candidate image was built from clean `git archive dbdbb4e7`:

- OCI index: `sha256:be28791b87cc6895984e68308dd168bf70f1e7cc9bb716a8f95c224cf3aa8eb7`.
- Linux/amd64 image config: `sha256:a7384b334771e8eb7dbbd14ead66be25fd448880f10ab356f89bb7b96cc7a51c`.
- Helm revision 164 uses the same chart bytes as revision 163 and the corrected
  manager/worker image. All six C9K workers converged on the exact image, all
  three virtual Nodes became Ready, and historical audit remained in place.

All **46** pinned Kubernetes 1.35.0 real-API tests pass on `dbdbb4e7`, with no
skips. An initial test invocation was interrupted while refreshing the upstream
asset index; the completed invocation used the already installed pinned assets.
That interrupted invocation is not counted as a pass.

## Strict HTTPS on the physical canary

At `2026-10-05T07:58:35Z`, `.103` generation 8 replaced its historical HTTPS
verification bypass with the opt-in reference. A dedicated Secret
`cat9k-lab-103-https-public-ca` contains only the independently trusted `ca.crt`
already used to validate this device's existing certificate. No private key was
copied and no device certificate or trustpoint was changed.

- Secret resourceVersion: `10573262`.
- Public bundle SHA-256: `4b4857727a6d1a2e58a53d80c691c070cdfcd231d59182f30b58411a7721a202`.
- Both actual worker Pods project exactly `ca.crt`, pass
  `--device-tls-ca-projection=v1`, and bind that same revision and digest.
- Both are Ready on image config `a7384b33…`; the manager accepted the exact
  network revision `sha256:a67b97162ce0e3157a00d2b97426fbe35c0f0b9864964e23547f4fa61776fefc`.
- An independent authenticated native-inventory GET completed at
  `07:58:52Z`, verifying the HTTPS chain and IP identity, with redirects and
  proxies disabled and no insecure fallback. Worker logs separately show a
  successful HTTPS IOS-XE connection and the original running 17.18.3 image.

The plan `roadmap-strict-dbdbb4e7-103` freezes only this C9300-24P,
UID `782dc4b2-7e43-4188-8436-87633f0bf4fb`, serial `FOC2416U0MV`.
Its exact approved plan hash is
`sha256:b96a7ccdb752bc70d57b8b9fcbfb81c9988ff834716111e4b6b4eb0b22b4952c`.
It uses PrepareOnly, complete network evidence, one transfer/unavailable slot,
and the administrator-derived 25,000,000 byte/s transfer ceiling. Source digest
is `c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355`.
The target is 17.18.02; no activation approval was issued. The leaf
`roadmap-strict-dbdbb4e7-103-cat9k-lab-103-1a40f940`, UID
`318e8ad5-fd43-4450-a8a7-ed8657c4c5df`, reached `Prepared` at `08:06:22Z`.
Its single primary-install claim was persisted at `08:04:51Z`. Exact versions:
running `17.18.03.0.5496.1776157760`, validated `17.18.02.0.4112.1766116039`.
The manager's continuous post-operation health gate succeeded at `08:07:31Z`.

Receipt: `sha256:4fabd8a8d1c932f24d12702f5d0094febe8e9d23c2896d551c6a2b388cb9cdb9`.
Its trust identity is
`sha256:7fe197d5959fa9a040035c9c9ec46a196205cfb9f363bd48b6f93043161fb665`.
Independent recomputation from the four runtime revision fields, including
HTTPS CA revision `10573262`, matches exactly.

Cancellation revision 1 was acknowledged with manager and worker `Settled` at
`08:08:21Z`. The exact-receipt retirement reached `PreparedInvalidated` at
`08:09:11Z`, with native proof
`sha256:63a6353f4aef5f42106ba12c6dac9c4a50c15c2aee7ee78f0696fe061284649f`.
The complete original receipt and claim array are unchanged. Native installer
history is identical immediately after preparation and after retirement: no
install replay, activation or software deletion. The ledger is empty and the
canonical mutation Lease has no holder.

The completed application collector recorded **360/360 HTTP 200** samples over
901.64 seconds, spanning migration and preparation but ending before the later
CA fault tests. Its five-second sampling is not a zero-loss forwarding claim.

## Public CA fault tests and follow-up hardening

On the idle `.103` canary, a deliberately malformed **public-only test copy**
was written at `08:11:57Z`. Both worker Deployments converged to empty CA mounts
and zero ready replicas. The dedicated copy was deleted at `08:16:05Z`;
both templates then carried the `missing` revision and remained unready.
Neither the original gNOI identity Secret nor device certificates were changed.
At `08:19:06Z`, the same independently trusted public bytes were recreated in
the dedicated Secret. Both workers recovered with revision `10580328`, the
Node became Ready, and a fresh verified-HTTPS native-inventory request succeeded.
Native installer history remained exactly unchanged through these fault tests.

Reviewing the direct projection identified a check/use race: kubelet could
refresh a mutable Secret into an existing worker between manager inspections.
No private-key exposure was observed, but a key accidentally placed in `ca.crt`
could evade the intended projection-time validation. The fix validates and
copies only public CA certificates into `device-ca.crt` in the **existing**
managed ConfigMap. Workers mount that public snapshot, never the source Secret.
Invalid source material removes the public snapshot. Source revision and actual
mounted-byte fences remain in force. There is no extra account, controller,
Secret-copy lifecycle or resource type. Configuration documentation explicitly
states that public certificates are readable through ConfigMap access.

A regression fails with the earlier direct projection and passes with the
snapshot. Native admission now rejects direct source-Secret CA mounts, and
the exact compiled contract rejects older managers. The snapshot implementation
still needs its own committed-candidate deployment and physical regression;
the `dbdbb4e7` results above must not be relabelled as that later candidate.

The two existing device applications continued returning HTTP 200 in the
sampled migration interval. That is not a forwarding or zero-loss guarantee.
There was no image activation or reload in this migration. App worker identity
rotation can interrupt Node status reporting while the device applications
continue running; these are different lifecycles.

## Raw capture integrity

Raw captures remain local and are not release artifacts; this document is the
durable sanitized account. SHA-256 values:

| Capture under `/tmp/` | SHA-256 |
| --- | --- |
| `cvk-20261005-ca-native-admission.log` | `fc7d71f10d8ad2604e8a3ef169d838e1e9ae24f923c324a169fc51dfc1dcd962` |
| `cvk-20261005-empty-preparation-before.log` | `9855cb9a1023417cc0e3cb765467f7bbe67adf974734c0edc76ca33f162c78cc` |
| `cvk-20261005-empty-preparation-after.log` | `8951886e8c5d4f39dd19bf9343cbfe4cd74ef7828153673df60a480995548276` |
| `cvk-20261005-empty-preparation-controller.log` | `7f54c659bde4090772e01a4a0b614246f5b440c1cb22e98b9800ecb53ed4d8bf` |
| `cvk-20261005-dbdbb4e7-race.log` | `7337149302eff506ab646b2e21a5373660616fc04116509fb43460c7dcefe319` |
| `cvk-20261005-dbdbb4e7-envtest-offline.log` | `0809d55fa1dc7809778cad9be4ce918a5fee8573ca77fbd311756f084a85fa36` |
| `cvk-20261005-dbdbb4e7-race-uncached.log` | `81136cadf04c92cac6b8a3c2bcfab85e665eb993316d8ffc8248db7c40a5e4cf` |
| `cvk-20261005-dbdbb4e7-native-admission.log` | `7d3d1d6aed7fbb765b64ce6fcd2bbd126ccc01e4aefb35e5d77bf7628b7a5e6a` |
| `cvk-20261005-strict-retire-progress.json` | `a27353321197639693e14ff045f9dc165fed92e447a5af846834026f66e56f47` |
| `cvk-20261005-ca-migration-probes.jsonl` | `b32769d79f8c8e6b7b4fccdd5fd957c8413719e4353388a0818513f7868d9044` |
| `cvk-20261005-ca-restored-inventory.json` | `2416dd983bc6c45d4cedb86ea93935fc27b6797a77df6d6d3ae3c20112f2b429` |

## Remaining qualification

Finish same-candidate strict HTTPS inventory and preparation/recovery, plus
the R3 drift/removal/replacement and both-direction tests. Revisit **all R0–R9**
before concluding: compatibility with retained objects, independent forwarding
calibration, portable app drain, grouped-drain implementation/physical support,
distribution measurements, second-platform qualification, sustained controller
scale, independently fenced ownership transfer, and six separately recorded
final lifecycle sequences remain distinct acceptance requirements. No gate is
waived by this focused fix.
