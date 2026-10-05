# Physical migration and preparation recovery follow-up

Date: 5 October 2026. This is a candidate-specific follow-up, not a replacement
for the immutable [acceptance checkpoint](README.md). Open roadmap gates remain
open until their individual evidence passes.

## Candidate and migration

The first fully converged migration candidate on Ubuntu16 was clean source
`79725f27bae26972a914a9679f0747321088e600`, image
`cvk-tas-extentions:79725f27`. The image was built from `git archive`, not a dirty
working tree. OCI index:
`sha256:08a424ec0ac46cf25cf78b00e8f10d367be1b3f257d861c48520a3cba2be2092`.
Its six workers resolved platform image ID
`sha256:cec1e60731e6a89f87fef636fe5ffdf898b0c7c21b7ef3754805cf13c957e3e1`.
The authorized physical cohort remains the three C9Ks on Ubuntu16; the Nexus,
historical duplicate Node and CI devices are excluded.

Migration first deployed `9229f755` with the two changed software CRDs and
matching native admission contract. Existing server-side field managers were
preserved; no forced ownership transfer or validation bypass was used. Two
historical audit compatibility defects were then reproduced on the live lab:

1. Old, terminal, settled drain records predated `requireNetworkEvidence`.
   Current validation repeatedly rejected their historical protocol binding.
2. Old, settled cancellation tombstones with no worker execution state hit the
   same generated-spec mismatch while their parent was paused.

`bcde632a` and `79725f27` fix these read-only compatibility paths. The complete
stored spec must match except for that exact newly introduced network bit.
Terminal drain compatibility additionally requires settled drain and resolved
mutation evidence. Tombstones must contain **no worker status or mutation
claims**. Existing identity, epoch and admission validation still applies.
Neither path rewrites a receipt, rearms a leaf, acquires a new reservation, or
relaxes active admission. Five minutes of live manager logs after deployment
contain neither historical error loop.

All six workers were Ready. Each app worker restarted once during new Pod
identity binding; network workers did not restart. A prior captured app-worker
startup shows fail-closed Node/status authorization before binding converges;
this is not evidence that device applications restarted. Existing device app
Pods remained running. This operational startup behavior should not be
represented as a zero-restart rollout.

## Automated results on the clean candidate

| Gate | Result | Local capture SHA-256 |
| --- | --- | --- |
| Full Go race suite, 61 packages | PASS | `c901850f290c7a496dd9a88481a99f22d3d63d615e1d29e350615dad83956ee2` |
| All 45 pinned real-API tests, no skips | PASS | `a1a6a50a1303b0d65955689aeb8e480b4d2a395a5d1350a73241ce8325443a61` |
| Real-API retained-claim/tombstone cases | PASS | `79c0a672d249ae25ee436476fb84ce062512ff856115e4b13834c0117ebc6dec` |
| Focused terminal/tombstone race regressions | PASS | `0fade41a99953a05cc58caa7208403239faf1b8b9b012f8251d40db18829dc16` |

The real-API cases preserve outstanding claims, exact reservations and safety
finalizers through missing/replaced policy, cancellation and deletion with
fresh reconciler instances. They do not run a device transport and therefore
do not prove native RPC recovery. Negative compatibility cases cover active
admission, unresolved claims, partial worker execution, changed source/target,
identity/control/epoch drift and staged intent. Live admission rejects old
protocols even where the terminal audit reader accepts the settled record.

## Physical preparation and integration failure

The exact-plan-approved campaign `roadmap-retirement-79725f27-103-r3` targets
only the scoped C9300-24P `.103`, preparing 17.18.02 while running 17.18.03.
The frozen plan hash is
`sha256:3002d1edfe01fd93c5828f2d739b89a993dc88319d64dfd31045223e8116553b`.
Source SHA-256 is
`c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355`.
The leaf requires fresh complete network evidence, uses the staged-activation
protocol, and has only a primary-install claim. **No activation is approved.**

Two earlier, unapproved planning attempts failed closed before convergence:
one lacked Ready gNOI configuration, the other lacked a manager-accepted
network sample. Neither created a leaf or authorized a device mutation. The
successful attempt was created after those prerequisites became healthy;
neither gate was disabled.

Preparation reached `Prepared` at 06:29:38 UTC. Secure gNOI Verify reported
running version `17.18.03.0.5496.1776157760` and validated target
`17.18.02.0.4112.1766116039`. Native CLI independently showed the original
image committed and the target inactive, with no abort timer. The receipt is
`sha256:2023644dd948631f803f8184c9c0186a974defcd2dd1f581f8463349f2bc8357`.
After the health soak, the campaign succeeded and both manager and worker
settled. Only the original primary-install claim exists; no activation claim
or approval was issued.

An attempted direct deletion of the idle network-worker Pod was denied by the
reserved-worker admission policy. The Pod was **not** replaced by that command;
the subsequent Deployment readiness response does not prove a restart. No
impersonation, admission change or container-runtime bypass was used. A normal
manager-owned rollout remains the supported replacement path.

Cancellation revision 1 reached `Cancelled`, acknowledged by the worker at
06:31:37 UTC. An exact-receipt recovery request then exposed a real runtime
integration defect: the dynamic driver wrapper did not forward the optional
`PreparationRetirementObserver` capability implemented by the IOS XE adapter.
The worker rejected recovery and retained ownership. No software was removed
or activated, and the receipt and claims remained unchanged.

Fix `b55157cedeb63fb08d4a5605e67220f77afa2241` adds the forwarding method through
the current-transport factory, preserving unsupported/error behavior. A new
regression failed before the fix with the exact missing-capability assertion.
Tests now cover context/receipt forwarding, transport replacement, missing
transport, factory failure, nil/unsupported adapters, native observation
errors, and the real IOS XE adapter's idle-positive/active-installer-negative
cases. All three affected package race suites and the complete race suite pass.
This fixes runtime wiring, not native evidence requirements. The existing
recovery request must still complete physically after deployment before this
record can claim recovery qualification. The following live result closes that
specific integration failure, not the entire R3 matrix.

## Successful native recovery after replacement

Helm revision **161** deployed exact runtime `b55157ce` through the normal
manager-owned rollout. The manager and all six scoped workers converged Ready.
OCI index is
`sha256:ac37ea18ea739b7c7754ab12c759fc53d5df026cf322d4c74192105de0f948bd`;
resolved platform image ID is
`sha256:6f95d55861df27b666e920d8aa5a8f7cd8b4a3388f1d828cd689aefbffe1dbac`.
The manager/network workers have zero container restarts; each new app worker
had one startup restart before binding convergence. Existing application Pods
remained running; the HTTP observations below are sampled service evidence.

At **06:41:23 UTC**, the replacement `.103` network worker recorded
`PreparedInvalidated`, with native target state `Installed` and unchanged
running version `17.18.03.0.5496.1776157760`. Native proof hash:
`sha256:c648e7b92e4e2cd46003b89f6189cf838eb2bf16829f176e4be99fcd4e8d4558`.
The parent remains `Cancelled` with `PreparationInvalidated=True`. The original
receipt is byte-for-byte equal after canonical JSON comparison. The only
mutation claim is the original primary install; recovery added no activation
claim. The ledger has zero reservations. Independent native CLI still shows
17.18.03 committed, 17.18.02 inactive and no abort timer.

A server-side **dry-run only** attempt to append activation approval to the
retired campaign was rejected by the CRD's no-activation-after-invalidation
rule. No activation approval was persisted. This is an actual admission
negative, not an attempted device activation.

The completed 1,803.5-second app probe collected **720 HTTP 200 responses and
zero errors**, at roughly five-second intervals per endpoint, spanning the
first preparation and runtime replacement/recovery. This is sampled reachability
of the existing two apps, not zero-loss forwarding or portable drain proof.

| Evidence on `b55157ce` | Local capture SHA-256 |
| --- | --- |
| Full race suite, PASS | `3d631852ae541fb27f0ebe81f045cef4cd201684a299941295c8ea489a79158d` |
| All 45 real-API tests, PASS/no skips | `ab2f42717f2682b74c9a639091a1fc161309dde1d2560b3b8093c7a33060848e` |
| Runtime-wrapper/real-adapter regression, PASS | `796bb045dba23132db8777db0b8f62c0e324e11095f618c8bfeed65298764185` |
| Retired leaf and immutable receipt | `5b8899fa1249931284fa6bf7ff1e7abd83032c8b7ebefcfa834a671f20f2eed5` |
| Cancelled parent with native proof condition | `827223a0cc3deec0764fc8fe183994e9fc060b73167c7de9c2d8742ad889a901` |
| Post-recovery native CLI | `a4ad3fd4f8b15855479da7b14665e61c78a91a685ea00fd3f06ea55ae0e7c00a` |
| Converged workers/manager and ledger | `bd0c328c3aaa69c95a62cf5b536c0230bcd169968a4bbfac136e1bd34aecda34` |
| Completed 30-minute app probe | `2c69b60aeae660248e3ded9db2b73a35f7a4017f5e6a4c31aae59e5ab7fa3afc` |

Raw captures are private `/tmp/cvk-20261005-*` files; this sanitized result and
its exact candidate/proof identities are the durable branch evidence. The
preparation ran on `79725f27` and recovery on `b55157ce`; do **not** label that
combined run a same-candidate final acceptance cycle.

## Fresh preparation and retirement completed without replay

Campaign `roadmap-fresh-b55157ce-103` uses the same exact qualified source and
single-device safety gates on `b55157ce`, but a new frozen plan:
`sha256:dc0148207e5a9e902b71f2e6350ed0cdfe82a5980a7957d65145d74b1e59aa9b`.
An independent exact-plan approval was recorded. A new leaf and new maintenance
request were admitted; the old settled maintenance acknowledgement was not
accepted for the new request. The new manager acknowledgement arrived at
06:46:52 UTC and the new primary-install claim at 06:47:59 UTC. No old receipt
was reused, and no activation approval exists.

The second attempt exposed a further IOS XE inventory shape: after a repeated
add, the version record retained `src-filename: /mnt/sd3/user/gNOI_iosxe_.bin`,
although that exact version's IMG package and successful new add history name
`gNOI_iosxe_17.18.02.0.4112.1766116039.bin`. The original correlator held the
attempt rather than treating the earlier add as proof. Native add UUID
`d6d104e1-bb22-4049-be1e-3ad598ae231d` completed at device-clock
06:47:10.145765; an HTTP-Date snapshot maps the switch clock approximately
130 seconds behind the lab host. The older add remains separately recorded.
No native remove, activation or repeated install RPC was submitted by the
operator to clear the hold. IOS XE's own gNOI processing records an inactive
cleanup before each add; that is device-side behavior, not CVK's recovery path.

Fix `dfe02ae43bd9ce721ab481bde05d970fc6fe5100` resolves **only that exact
placeholder** in the read-only correlation path. Every location must contain
one matching exact version and one added IMG package, with the precise
version-derived filename and the same native directory. Verified source size,
all-package added state, quiescent installer, native clock mapping and one new
completed add in the claim interval remain mandatory. Empty, arbitrary,
ambiguous or mismatched filenames are not normalized. Generic inventory and
device files are not rewritten.

The new positive regression failed before the fix. Fourteen positive/negative
cases, the minimized captured-inventory test (including later retirement),
all affected race suites and the complete race suite pass. Full race capture:
SHA-256 `5ea477d64ca6385696b378c65841d2d2570a8de9c94abf3216e1dbb807d66110`.
Native fixture tests:
`5cc96c0a605b4f6d66b04fe00380b34a93f7ca858f0ae509eb9dcff1761cfe85`.
All 45 pinned real-API tests also pass without skips (capture SHA-256
`c1f9728b1c426d1ccde219e5e9b608215609053e261496f4bca0918919963644`).

Helm revision **162** deployed exact source `dfe02ae4`, OCI index
`sha256:318e08ee51483c09d8dccddc57d3f5f744f3ea3a159e23017fc0bde0b403b0b5`.
The manager and all six scoped workers converged to platform image ID
`sha256:c7d7b8c587583b17e0771dcd5b49f0cfaa382c053cd8888c5656d4478c4a8382`.
The original outstanding install claim and device Lease remained intact
during replacement. The new worker correlated the completed native add;
it did not dispatch another install.

At **07:02:20 UTC** the leaf reached `Prepared` with a new receipt:
`sha256:7929c73fb5831f512ddda817c83feb11dfb7e7ff52109c38f70fcce3c5110c1d`.
Running version remained `17.18.03.0.5496.1776157760`; the exact validated
target was `17.18.02.0.4112.1766116039`. The parent completed its health soak
and reached `Succeeded`. Cancellation revision 1 was acknowledged and settled
at 07:04:54 UTC. A separate exact-plan/leaf/receipt-bound retirement request
then reached **`PreparedInvalidated` at 07:08:10 UTC**, with native proof
`sha256:79dbc629f76bdf1fdb82144d6409877530a1a4aa34cf6eaab9770322d50ccaac`.
The parent is `Cancelled` with `PreparationInvalidated=True`.

Canonical JSON comparisons confirm that the prepared receipt and the single
primary-install claim are unchanged after retirement. Native operation-history
arrays are identical before and after recovery/retirement: exactly two completed
adds across the two campaigns, with no third add or activation. Final CLI shows
17.18.03 committed, 17.18.02 inactive, and no abort timer. All three Nodes are
Ready; both existing app Pods are Ready with zero restarts. The ledger has zero
reservations and the device mutation Lease has no holder. No activation approval,
manual image removal, forced Lease clearance or admission bypass occurred.

| Final fresh-run evidence | Local capture SHA-256 |
| --- | --- |
| Cancelled parent and retired leaf | `02763a81099f5a1bfa50e519c53e4ebf82f4f1773c6e82558cfa73d71a3a1dd9` |
| Final Cisco CLI and native inventory | `603dff968913f3eceda3b8f024b75ddc4900cf5bb60f7cc93e9b63cca1484634` |
| Ready runtime/apps, empty ledger and released Lease | `1f83e0e80e2f085b9bc716cce78f96ea6546f8d60fe7134aecc48bcb6ac4ca9e` |
| Completed 15-minute replacement/recovery app probe | `787ad5603940d29204d0b8098103e3038acb1a17c92f82f661d92143379359d4` |

The second completed probe spans 901.8 seconds from 06:58:29 UTC through
runtime replacement, fresh receipt, cancellation and retirement: **360 HTTP
200 responses, zero errors**, with an explicit collector-complete record.
It measures sampled reachability of the existing apps, not forwarding loss or
portable replacement continuity.

This is a successful **mixed-candidate recovery test**: `b55157ce` dispatched
the new install and `dfe02ae4` observed/retired it. It is not the final
same-candidate, three-device, both-direction activation matrix.

### Transport qualification limitation

The scoped `.103` CiscoDevice retains the lab's pre-existing RESTCONF
`tls.insecureSkipVerify: true`. It was not changed for these tests. gNOI uses
TLS with the provisioned trust configuration, and CLI checks use the existing
strict SSH host key, but those do **not** authenticate the separate RESTCONF
response. The native-inventory diagnostic used the existing device transport
policy, with no credential output or redirects. Its raw capture SHA-256 is
`af347c220fbd38f602c4520dcabef092614b8ba009b743bd45c65aa7be4caf45`.
At 07:10:01 UTC, a separate credential-free TLS handshake to `.103:443`
**successfully verified** the existing provisioned CA chain and IP SAN
`198.51.100.103`. The server already uses the certificate issued by the CVK
gNOI C9K103 intermediate. No certificate/trustpoint replacement is needed for
this endpoint. This does not retroactively authenticate earlier requests or
change the worker's `insecureSkipVerify` setting.
The separate handshake capture SHA-256 is
`a8cf830eb49caeb4782e6536b48f63b8bcf7e60b32ae68e02ac55230fed23495`.

Worker inspection identified the remaining integration constraint: the network
worker projects the existing CA, but the app worker intentionally omits all
gNOI projections. Both share the device RESTCONF configuration. Pointing its
`tls.caFile` at the network-only gNOI mount would therefore break app-worker
startup. Do not expose gNOI signer/client keys to the app worker or patch
manager-owned Deployments as a shortcut. A public-CA-only device TLS projection
with both-plane rotation/fencing tests, or an independently provisioned common
trust store, is needed before enabling strict verification for both workers.
No runtime TLS setting was changed in this follow-up. Retest authenticated
native inventory/recovery before closing complete production trust qualification.

## Merge disposition and next execution boundary

The branch is not behind `origin/main` as checked after this run. Required
review on PR #197 is still absent. CI for runtime `dfe02ae4` is run
[`37275024056`](https://github.com/cisco-open/cisco-virtual-kubelet/actions/runs/37275024056);
at 07:10 UTC five checks had passed and build-and-smoke was still executing
real-API tests. This is a time-stamped observation, not a final CI pass.
The final documentation build passes in strict mode and all 36 Python tests
pass. Later heads need their own required checks.

Do not merge as complete-roadmap delivery on this evidence. Continue from
R1–R9 in the [execution plan](../../topology-roadmap-execution.md#remaining-completion-plan):
mixed-version/rollback qualification, verified worker RESTCONF, the remaining
drift matrix, independent path/headroom and portable-app qualification,
opt-in grouped-drain implementation, distribution/scale measurements,
second-platform qualification, independent device-fenced cross-cluster
transfer, and the final same-candidate six-cycle lifecycle matrix.
Some are code/test work, not external blockers. The second-platform image
pair and independently credentialed destination cluster are not currently
authorized lab fixtures; do not use the existing unrelated Nexus/CI targets
to manufacture those passes. Approval or CI cannot substitute for these gates.

## Remaining scope

R1 mixed-version/rollback qualification, R2 calibrated forwarding/headroom,
R3 image drift and both directions, R4 portable app continuity, R5 grouped
drain implementation/qualification, R6 repeated distribution measurements,
R7 second-platform qualification, R8 sustained controller scale and independent
cluster/device fencing, and R9 final same-candidate six-run acceptance remain
explicit gates in the [execution plan](../../topology-roadmap-execution.md).
Green CI and this migration do not close those gates or provide human PR
approval.
