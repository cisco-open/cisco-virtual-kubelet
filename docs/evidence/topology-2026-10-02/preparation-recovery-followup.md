# Preparation recovery follow-up — 4 October 2026

This is a development-branch implementation and test record, **not a claim of
merge readiness or physical roadmap completion**. The last deployed C9K
runtime remains `6f3686e9`. PR #197's six checks passed on `583a8ca3` before
these changes; that CI result does not cover this follow-up.

## CI security follow-up

On `f88afad1`, five remote checks passed; build-and-smoke stopped at the website
audit in run `37229240762`. The lint-only fast-glob → micromatch → braces chain
was affected by GHSA-vfj7-8cjw-p6xm, with no patched braces release at review.
`9823ee09` replaces that single dependency with a small, tested directory-glob
adapter using tinyglobby. Direct aliasing failed compatibility tests and was
discarded. No audit waiver, major Next downgrade or CVK runtime dependency
change was made. Local-link classification was corrected in the license
collector; the 19 production-package/4 vendored-entry notices are unchanged.

The exact clean-install website lane passes: three adapter/actual-Next-rule
tests, ESLint, audit (zero vulnerabilities), license checks, production build
and built-output notices. Raw log SHA-256:
`197399a00370dcf03772b27fda62e97c386ac8a46e145b1ca6cee98ff5ab9cd4`.
Remote CI must still complete on the next pushed head; the earlier failed run
is not a pass, and local results do not substitute for required remote checks.

The next smoke run (`37230837385`, source `60db561d`) passed that website gate,
then exposed a stale assertion in `topology-kind-test.sh`. A bound worker's
attempt to change `managerDrain` was correctly denied, but the test still
matched the old message without the newly protected `managerInvalidation`
field. Local line-level tracing reproduced the failure at the message check,
not at the authorization request. The assertion now checks the current exact
manager-owned field message; no admission permission was relaxed. Unhandled
test failures now report their line number without printing token-bearing
commands. The **entire** baseline real-API topology/migration/drain/scheduler
suite passes after the repair, not merely the formerly failing assertion.

Additional final-source validation on `3dd991ae`: all **44** pinned API tests
and the affected controller/provider/IOS XE lifecycle race suites pass. Its
native-TAS remote check also passes, including real Deployment ownership and
controller-process restart. These are not new physical lifecycle results.

| Final capture | SHA-256 |
| --- | --- |
| Full baseline topology integration after assertion repair | `db4b5e77d86c49175217ebff5be4a37e105ab828a9c2c5694c48fa0b0a80cf98` |
| Final pinned 44-test API suite | `ff271fdafc46f365268a39df9142a55179edc9c2addc304e300af33bc5843511` |
| Final affected runtime race suites | `a83b7eae246b06ed395f23952a09217406e04cfeabdcb343c6c89f780119a2cf` |

## Change and security boundary

An operator can abandon selected exact receipts from a cancelled, settled
PrepareOnly campaign that never authorized activation. Native admission binds
the asserted actor to the authenticated caller with the distinct `recover`
verb. The manager publishes immutable authority; only the bound network
worker can append native proof. The app-hosting account gains no permission.
The existing platform-neutral lifecycle capability boundary owns native
inventory interpretation; the manager never opens device sessions.

The worker observes under the normal device mutation Lease and a deadline
shorter than its lifetime. It verifies native installer quiescence, committed
original running software, exact target absence/inactive installation, gNOI
Verify and unchanged live authority. The original receipt stays immutable.
`PreparedInvalidated` is irreversible and cannot authorize old activation.
Queue and device handoff release share a single proof predicate. Unknown
claims, active/ambiguous images, failed Verify, missing capability and
dual-supervisor cases remain held. No recovery code sends an install,
activation, image-removal or configuration RPC.

## Automated evidence

The focused tests cover request-only non-release, wrong receipt/plan/incarnation,
unacknowledged cancellation, unsettled outcomes, prior activation, stale proof,
changed running software, incomplete inventory, manager/worker ownership,
original receipt preservation and zero install/activate calls during recovery.

The pinned real Kubernetes API test applies the actual Helm-rendered rollout
policy and RBAC. It verifies a planner cannot recover, an authorized recoverer
cannot impersonate another actor, and exact authorized recovery is accepted.
It also exercises append-only receipt/request/native evidence and irreversible
phase transitions, including an old-writer update omitting the new fields.
This is API execution, not string matching or a fake authorization check.

Local raw logs (not published):

- `/tmp/cvk-invalidation-race.log`: full Go race suite.
- `/tmp/cvk-invalidation-envtest-final.log`: full pinned Kubernetes 1.35 API suite.
- `/tmp/cvk-invalidation-native-auth.log`: focused native recovery authorization/audit test.
- `/tmp/cvk-invalidation-shared-admission.log`: all native shared-account policies on disposable Kubernetes 1.35.
- `/tmp/cvk-invalidation-helm.log`: render/preflight policy contract.

Results: full race PASS; affected packages rerun under race PASS; all **44**
pinned real-API tests PASS; focused native authorization plus expanded immutable
audit test PASS; complete native shared-account admission PASS; Helm/preflight
contract PASS; strict MkDocs PASS. The later lease-expiry, foreign-owner and
control-CAS tests also PASS under race. Native recovery device execution is
still a separate open gate.

SHA-256 evidence identifiers:

| Log | SHA-256 |
| --- | --- |
| Full race | `cc3424787e0f8ce5bda5473fb6ac75392536c27b1bb83c779c007fd519d4f24a` |
| Full 44-test API suite | `b43fc1c768ebe900c11b4853ad57d3d5cae7721a2a3de8be7ead99dd4f52ad0a` |
| Expanded native authorization/audit | `66ea5890ddf4dee38940aa5b6b3382584c515d81e88ccfc862e82d4b544a544b` |
| Shared-account admission | `75bf6edcdcefdad1844ca108f080516ee56ea6ca5c6c73e7b39fbb6489187796` |
| Helm/preflight | `45125a6a68b0ff4a66c7a19aaabc92689434a060a46fd79e69c17d34744881d1` |
| Lease/control races | `97c18525dcd3ca5cdab1725f3f97b4f800f2a8f9ebda74489cc0f5f6317d807f` |
| Final affected race regression | `1beadb0117cd3154c1a5e2a1e56fae99ecd7a887f4b96566b432513bc324f62a` |
| Final native authorization/audit regression | `90bf7c51ebd7eca99b1713641f8584285924ef7cfaf9cf86964a68cbcceb73dc` |
| Completed-Reload isolation and recovery | `ce6cabfb8ad22f70c61b0425a66a072b2a49b238699924e47cacb082ed036264` |
| Final Helm/preflight | `8f7ebe4f856f54d4a5a4da8118cdb2a06c179606e4dd50aaff8206a0700c395a` |
| Physical read-only app/install capture | `886d4defe0a12b446dbdf6a1c2dc80d2379bc9f5c68d1ce738c130b051bc7892` |

## Actual released-manager startup boundary

The disposable shared-account suite now builds the **unchanged** October
manager from `cf33e51c8ffc6d47acb313857665366d74eefe6c` and runs its real
entrypoint in a non-root Pod using the chart-rendered manager ServiceAccount.
Against the newer native policies it exits unsuccessfully with:

```text
Error: managed topology native admission preflight: ValidatingAdmissionPolicy
"cvk-shared-worker-it-cisco-virtual-kubelet-managed-node": variables differ
from the compiled contract
```

Policy and ledger ConfigMaps compare byte-for-byte unchanged before/after.
The full shared-account suite then passes on Kubernetes 1.35.0, including
retained Leases, stale tokens, split app/network writes, drain-only authority,
native unhealthy-node updates and foreground deletion guards. The disposable
cluster is removed by its owner on exit.

Reproduce with:

```sh
git fetch --no-tags origin cf33e51c8ffc6d47acb313857665366d74eefe6c
bash charts/cisco-virtual-kubelet/tests/managed-shared-worker-kind-test.sh \
  --cluster-name cvk-rollback-qualification
```

The helper is `scripts/test-released-manager-startup.sh`. This closes the
**actual old-manager startup negative** subtest, not the full R1 interrupted
migration/mixed-version/operational rollback matrix. No physical controller
was replaced. An early local harness failure did not reach this assertion;
the completed result below supersedes it.

| Capture | SHA-256 |
| --- | --- |
| Full disposable shared-account/startup suite | `4ceb399fb14c226ad2f0241984a977dfccb4f046921396ac1aa9e2e058276c90` |
| Released manager Pod log | `331f2edae85bdd13b5c23c02403ff179f7f6444e19e4c1b808258658d336b7d5` |

## Physical read-only follow-up

Ubuntu16 reports all three authorized physical C9Ks Ready on 17.18.03. A fresh
trusted-host-key SSH read of the previously failing app-hosting switch confirms
no installed app, USB-backed IOx with its pre-existing signing setting, and
committed 17.18.03. Its current install log shows successful boot. The filtered
CAF/IOx log did not retain an activation-failure diagnosis. This does not
establish the cause of the earlier app activation timeout or qualify a portable
replacement workload. No signing, boot, certificate or device configuration
was changed during this read-only check.

### Existing app fixture retry: successful, not a diagnosed fix

After confirming the three device mutation Leases were unheld, the existing
second test Deployment was scaled from zero to one replica. Its original
manifest scheduled to the previously failing C9K. Native logs now show
`DEPLOYED` → `ACTIVATED` → `RUNNING`; native detail confirms the custom
CPU/memory/disk profile and management vNIC. This uses the unchanged `6f3686e9`
runtime, not the new recovery binary. The previous failure was not reproduced;
do not attribute the success to an unmade code fix or claim its cause is known.

The second switch uses DHCP. Its initial Kubernetes PodIP was `0.0.0.0` while
address discovery converged, then matched the actual native interface address.
An initial probe at the previously assumed/static address timed out; the probe
at the address reported by the device returned **HTTP 200**. The original
first-switch app also returned **HTTP 200**. Both Kubernetes Pods are `1/1
Running`, and the existing `minAvailable: 1` PDB allows one disruption.

This establishes a fresh two-replica **baseline**, not continuity during
eviction/reload, redundant forwarding, or image portability to internal-storage
hardware with signature enforcement. Both replicas are left running for the
next controlled drain test; the second Deployment's starting replica count
was zero. No app signature setting or trust anchor was changed.

A subsequent read confirms all three C9K Nodes Ready and both Pods still
running without restarts. The `.101` CiscoDevice retains its pre-existing
RESTCONF `tls.insecureSkipVerify: true` lab setting. This is **not** verified
RESTCONF server-identity evidence; trusted-host-key SSH captures and the
separate gNOI TLS configuration must not be conflated with that setting.
No TLS setting was weakened or silently changed to obtain these results.

| Capture | SHA-256 |
| --- | --- |
| Native install/activate/start and initial probe | `f407d8561829d825a725f690463c97089bbff2b5ae5f0d1ba4e53f02c2552418` |
| Native profile and DHCP interface | `04b07b0e2a658cdf10a947f731287e89082757f0c505a2ddfccd85aa8ee49b3d` |
| Actual endpoint HTTP, Pod and PDB baseline | `6712b71fe50f469f9f87633e0c56f972d1f4268e51ace6f3d73b8e73d556809d` |

## Remaining acceptance work

1. Finish candidate CI and preserve exact image/build provenance.
2. Qualify the interrupted schema/policy/runtime upgrade and operational
   rollback matrix (R1); retain cancellation and invalidation audit fields.
3. Deploy with existing campaign execution fenced, verify native bindings,
   stage an isolated inactive image, prove drift blocks old intent, then run
   explicit recovery and fresh-plan/receipt approval. Exercise worker/manager
   restart and both directions. This physical R3 gate is not yet passed.
4. Preserve the now successful two-replica baseline, qualify cross-node
   replacement under drain and real spare placement, and measure service
   continuity under PDB drain. Investigate if the prior activation timeout
   recurs; a successful retry alone does not explain that earlier failure.
5. Supply/qualify an independent forwarding/load/redundancy fixture within
   the authorized lab. The observed three-switch chain is not redundant-path
   proof. Do not fault outside infrastructure or waive continuity gates.
6. Run all six separately authorized prepare/hold/approve/activate sequences
   on one exact candidate, collecting path, service, PDB, restart and final
   health/ledger evidence. Wider R5–R8 gates remain as specified in the roadmap.

Do not merge merely because an earlier commit's checks are green. Do not
reclassify hardware prerequisites or unexecuted tests as successful.
