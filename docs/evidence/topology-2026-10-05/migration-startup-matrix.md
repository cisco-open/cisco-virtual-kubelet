# Migration startup and retained-audit qualification

Date: 5 October 2026. This record supplements the fixed-candidate
[acceptance checkpoint](README.md); it does not turn its blocked rows into
passes. Source under test: `a905097081ba38faf4ba2f2912fd5f7e9d204b2a`
plus the test-only changes committed with this record. No production runtime,
CRD, policy, RBAC or physical-lab configuration is changed by this follow-up.

## Tests and boundaries

The shared-worker kind suite invokes the real manager executable inside a
non-root Pod with the rendered manager account, projected credentials and
leader election. The cluster is disposable, has no CiscoDevices or campaigns,
and cannot dispatch device operations. Native admission is exercised by the
Kubernetes API, not a mocked evaluator.

| Case | Required observation |
| --- | --- |
| October release `cf33e51c8ffc6d47acb313857665366d74eefe6c` under current policies | Exits at admission preflight with the compiled-variable mismatch; never starts the manager; policy and ledger unchanged. |
| Lab runtime `6f3686e98dad0ca4f30f8f21453d57683170537e` under current policies | Exits at admission preflight: rollout policy has 9 validations, old runtime expects 7; policy and ledger unchanged. |
| Current manager with Warn-only binding | Exits because the binding must enforce only Deny; authority unchanged. |
| Current manager with a missing binding | Exits identifying the missing required binding; authority unchanged. |
| Current manager with a partially updated policy | Exits identifying the compiled contract digest mismatch; authority unchanged. |
| Complete current contract, initialized empty ledger not yet bound to policy | Starts, becomes Ready, acquires its actual leader Lease and binds the existing ledger without changing that ledger. |
| Manager replacement after binding | New Pod acquires leadership normally; zero container restarts and byte-identical policy/ledger before and after. |
| Actual October typed serializer writing a current `Prepared` leaf | Its lossy status round trip is rejected by the retained CRD; receipt and resourceVersion unchanged. |
| Same old serializer writing a `PreparedInvalidated` leaf | Same denial; retained receipt and invalidation audit remain unchanged. |

The serializer is compiled against the archived released module and API, not
against copied or current types. It has no cluster credentials; the test
submits its JSON to a real API server. The test checks the exact receipt
preservation error, not merely any failed write.

## Reproduction

Fetch the three pinned historical sources used by the compatibility suite:

```sh
git fetch --no-tags origin cf33e51c8ffc6d47acb313857665366d74eefe6c
git fetch --no-tags origin 44d02a4b1f17c832e79ea554e7f77795e4472fc4
git fetch --no-tags origin 6f3686e98dad0ca4f30f8f21453d57683170537e
make test-envtest
CVK_SHARED_WORKER_KIND_IMAGE='kindest/node:v1.35.0@sha256:4613778f3cfcd10e615029370f5786704559103cf27bef934597ba562b269661' \
  bash charts/cisco-virtual-kubelet/tests/managed-shared-worker-kind-test.sh \
  --cluster-name cvk-migration-qualification
```

Use an unused cluster name. The parent suite owns and deletes only its test
cluster. The helper checks its kind container identity and refuses a populated
device/campaign inventory before changing any test admission binding.

## Results and interpretation

All 44 real-API envtests passed, without skips. This includes both released
typed-writer denial cases. Local full-suite capture:
`/tmp/cvk-20261005-migration-envtest-full.log`, SHA-256
`92102c3985e3de89b43ef8f7f4a40f761cb380cb887087e5aa35c8467d789dae`.
All seven startup cases and the complete shared-worker native-admission suite
passed on pinned Kubernetes 1.35.0 (exit 0), including the exact lab runtime
and interrupted-bootstrap midpoint. Local capture:
`/tmp/cvk-20261005-migration-matrix-verified.log`, SHA-256
`a7a3c8c5af8c95a605ac8a6a9a7c41dd30224497a9a44fc107668a0ed4828986`.
The disposable cluster was removed by its owning suite. All 36 Python
safety/accounting tests, strict MkDocs, shell syntax and diff-whitespace checks
also passed. Raw captures are local; this sanitized result table is the durable
record. No result here is physical-device qualification.

Earlier harness failures were test defects, not reasons to weaken protection:
the positive startup assertion used the wrong capitalization for a leader log
(now checks the actual Lease holder); a whole-object replacement modified
protected ConfigMap metadata (now uses a data-only, resourceVersion-guarded
patch); and the old lab manager correctly reported validation-count mismatch
rather than the test's expected digest text.

All six remote checks passed on prior head `a9050970`, run `37266945449`.
These new tests require their own remote CI; the earlier run does not cover
them. The physical runtime remains `6f3686e9`, Helm revision 157.

## Operational consequence and remaining merge gates

An old manager exiting under new policies is a **safety stop**, not a working
rollback. Do not use unrestricted `helm rollback`, remove validation, delete
receipts or clear claims to make that manager start. Keep the new storage
schema and audit protection; restore a matching complete new contract/runtime
when recovering a partial deployment. Quiesce new campaigns and observe
already claimed work before changing deployed versions. Empty-ledger startup
does not prove safe migration while a device mutation is outstanding.

R1 remains open for the mixed-manager/worker operational matrix, interrupted
deployment with unresolved claims and feature-disable/rollback with retained
objects. Complete those tests before deploying new recovery code to the lab.
Then qualify R3 physical retirement/drift recovery, R2 independent forwarding
and headroom protection, R4 portable application continuity and R9 the six
candidate-bound device/version lifecycle runs. R5 group drain still needs
implementation; R6 repeated distribution/resource measurements and R8 scale
qualification still need execution. R7 second-platform images and R8
independent-cluster fencing require qualified fixtures outside the three-C9K
cohort. None of these gates is closed by green CI or this startup matrix.
