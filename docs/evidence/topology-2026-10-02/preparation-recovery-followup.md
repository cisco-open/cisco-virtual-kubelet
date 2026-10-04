# Preparation recovery follow-up — 4 October 2026

This is a development-branch implementation and test record, **not a claim of
merge readiness or physical roadmap completion**. The last deployed C9K
runtime remains `6f3686e9`. PR #197's six checks passed on `583a8ca3` before
these changes; that CI result does not cover this follow-up.

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

## Physical read-only follow-up

Ubuntu16 reports all three authorized physical C9Ks Ready on 17.18.03. A fresh
trusted-host-key SSH read of the previously failing app-hosting switch confirms
no installed app, USB-backed IOx with its pre-existing signing setting, and
committed 17.18.03. Its current install log shows successful boot. The filtered
CAF/IOx log did not retain an activation-failure diagnosis. This does not
establish the cause of the earlier app activation timeout or qualify a portable
replacement workload. No signing, boot, certificate or device configuration
was changed during this read-only check.

## Remaining acceptance work

1. Finish candidate CI and preserve exact image/build provenance.
2. Qualify the interrupted schema/policy/runtime upgrade and operational
   rollback matrix (R1); retain cancellation and invalidation audit fields.
3. Deploy with existing campaign execution fenced, verify native bindings,
   stage an isolated inactive image, prove drift blocks old intent, then run
   explicit recovery and fresh-plan/receipt approval. Exercise worker/manager
   restart and both directions. This physical R3 gate is not yet passed.
4. Diagnose the second app replica, qualify real spare placement and a
   portable trusted workload, and measure service continuity under PDB drain.
5. Supply/qualify an independent forwarding/load/redundancy fixture within
   the authorized lab. The observed three-switch chain is not redundant-path
   proof. Do not fault outside infrastructure or waive continuity gates.
6. Run all six separately authorized prepare/hold/approve/activate sequences
   on one exact candidate, collecting path, service, PDB, restart and final
   health/ledger evidence. Wider R5–R8 gates remain as specified in the roadmap.

Do not merge merely because an earlier commit's checks are green. Do not
reclassify hardware prerequisites or unexecuted tests as successful.
