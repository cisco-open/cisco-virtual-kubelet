# Migration and diagnostic validation follow-up

Date: 2 October 2026. Compatibility tests: `bfb826f0`. Diagnostic fix:
`3135a205`. This closes specific defects and test gaps, **not overall merge
readiness**. The deployed lab manager/workers remain on `6f3686e9`; no new
image upgrade, downgrade, receipt invalidation or service-continuity pass is
claimed here.

## Compatibility results

`scripts/test-staged-protocol-compat.sh` now compiles both manager and worker
tests against the exact October release
`cf33e51c8ffc6d47acb313857665366d74eefe6c`. The manager accepts the legacy
`rollout-v1` positive control and rejects byte-pacing, staged-activation and
network-evidence protocols in both Granted and Settled states. This tests the
actual old validation function, not a reproduction in current code. It is not
a full old-manager deployment/restart test.

`TestEnvtest_TopologyStoredNeighborMigration` installs the exact historical
CRD from `44d02a4b1f17c832e79ea554e7f77795e4472fc4`, persists a device and
neighbor observation, and proves that the old served map-list schema rejects
duplicate display IDs. It then installs the current CRD, checks preservation
of the device UID and stored observation, and successfully writes two distinct
source/interface-qualified neighbors with the same display name. Re-reading
the object confirms that migration did not invent accepted evidence or a fresh
sample sequence. The October schema did not have these network fields; this
is the intermediate branch's map-to-atomic migration, not an October schema
feature.

The migration passed once in the complete **43-test** real-API lane and three
additional consecutive runs. CI fetches the historical object explicitly.
A missing Git object is a failure, not a skipped migration test.

Still required under R1: interrupted CRD/policy/worker deployment, actual
reverse-manager operation, and supported rollback/retirement qualification
with both settled and unresolved new-protocol state. These tests do not make
arbitrary CRD downgrade safe. Keep the existing paused, forward-only deployment
order and retain all claims, receipts and approvals.

## Physical diagnostic false-success defect

On the current lab runtime, `verify /sha256 flash:nginx.tar` returned the
native `% Invalid input detected at '^' marker.` response, yet its
`DeviceOperation` reported Succeeded. The transport had mistaken a completed
SSH exchange for a successfully accepted CLI command. That operation is **not
valid checksum evidence**.

The IOS XE diagnostic transport now marks bare invalid, incomplete and
ambiguous parser responses as command errors. The transcript still passes
through the existing redaction/truncation path; the error message contains no
arbitrary device text. Ordinary log/config output containing error text is not
classified by a broad substring match. A reconciler regression verifies that
the command error produces Failed while preserving the output evidence.

The opt-in `TestLabSSHDiagnosticRejection` ran successfully against all three
authorized physical C9Ks. Each run sent only:

```text
show version
show cvk-readiness-nonexistent
show app-hosting list
```

The first and last commands succeeded; the deliberately unsupported middle
command was classified as a rejection. SSH verification used existing trusted
host keys, not trust-on-first-use or an insecure fallback. Credentials flowed
from the existing Secret into test stdin without being logged or stored in
the branch. The test binary's source matches `3135a205`; it ran directly on the
lab host and did **not** replace deployed workers. End-to-end DeviceOperation
behavior was tested with the reconciler fixture, not a newly deployed CR.

To build the repeatable, read-only hardware probe:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -tags lab -c \
  -o /tmp/cvk-ssh-diagnostic-lab.test \
  ./internal/drivers/iosxe/configdriver/transport
```

Run that binary on the authorized lab host with
`CVK_RUN_SSH_DIAGNOSTIC_LAB=1` and
`-test.run '^TestLabSSHDiagnosticRejection$' -test.v`. Supply a single JSON
object on stdin with `Address`, `Username`, `Password`, and `KnownHostsFile`.
Use the existing secret-management path; never paste credentials into shell
arguments or check in connection JSON. Missing inputs or host-key mismatch
fail the test. Building/running with `-tags lab` is explicit and is not part of
the ordinary unit-test lane.

## App fixture diagnosis

The supported `verify /sha512 flash:nginx.tar` command returned identical
128-character digests on the working and failing app targets. Thus different
package bytes do not explain the observed activation failure. Fresh native
checks show stable CAF, available resources, up app-facing/management
interfaces, and no remaining failed app. The working app uses its custom
profile; the earlier failed app remained DEPLOYED with the default profile.
That difference is an observation, **not an established root cause**.

The activation failure remains unresolved. The current cohort still lacks a
qualified portable service fixture with spare eligible capacity and an
independent, calibrated forwarding/redundancy/load fixture. Do not count
successful SSH queries, Ready Nodes, equal archives, or discovered adjacencies
as physical drain or forwarding-continuity evidence. No signing policy, trust
anchor, device configuration, mutation Lease or retained receipt was changed.

## Validation and evidence

| Gate | Result |
| --- | --- |
| Actual October manager/worker protocol probes | PASS |
| Stored neighbor schema migration, including old-schema negative control | PASS; three additional consecutive repetitions |
| Complete pinned Kubernetes 1.35 envtest lane | PASS; 43 top-level tests |
| Full `go test -race -count=1 ./...` | PASS |
| Diagnostic transport and operation regressions | PASS under race |
| Read-only physical diagnostic probe, three C9Ks | PASS |
| Modified workflow syntax and `git diff --check` | PASS |

Raw lab outputs remain local. Reproducibility references:

| Artifact | SHA-256 |
| --- | --- |
| `/tmp/cvk-ssh-diagnostic-lab.test` | `813cb9486956d4a4ba7cd5432d2666b58a4c0ca0924b5ea689e0e5ab14075be6` |
| `/tmp/cvk-merge-cli-physical-cohort.log` | `4e619ef748e0cfc33bf615e4f1deb82584e8a82b06f616437077b4937388deca` |
| `/tmp/cvk-merge-migration-cli-race.log` | `85cfe124912e4477cb4847b6540d07b107b49053e5d8c2f09a546a74102b7549` |
| `/tmp/cvk-merge-migration-envtest-full.log` | `5bf340598173eeffc22beada41f6188f9dac30f0582e197838781844698dc804` |
| `/tmp/cvk-merge-manager-worker-compat.log` | `9c8ec06c72d7c761a1b9ba33fcc59e793cedf04622dbb41a85a95ee007abf120` |
| `/tmp/cvk-merge-stored-migration-repeat.log` | `a2fea7cce7805458e5942d98d256f4a27907ab19ea9d8f430643fc2fcc8c795a` |

## Outstanding merge gates

### Documentation dependency security follow-up

GitHub's three open default-branch alerts also affected this branch's
`urllib3==2.7.0` documentation dependency. The lock now selects `2.8.0` with
generated hashes and an explicit security floor; its redistributed license
notice was regenerated. Upstream identifies this release as the fix for
[HTTPS proxy TLS handling](https://github.com/urllib3/urllib3/security/advisories/GHSA-8988-9cw3-xx77),
[unbounded chunk headers](https://github.com/urllib3/urllib3/security/advisories/GHSA-vxq7-64xx-v4gw),
and [deflate streaming loops](https://github.com/urllib3/urllib3/security/advisories/GHSA-gh4c-6fx4-qh6g).

The entire hashed Python dependency closure passes `pip-audit==2.10.1` with
`--strict --disable-pip --require-hashes`; there are no ignored advisories.
Smoke and documentation deployment now run the same audit in an isolated
tool environment, without adding auditor dependencies to the shipped site.
Strict MkDocs and license consistency checks pass after the update. This
affects documentation tooling, not device authentication or controller runtime.
Default-branch alerts remain open until the fix is merged and rescanned.

### Remaining acceptance work

1. Finish R1's interrupted-deployment and rollback matrix. The two newly
   passing subtests must not be reported as complete migration qualification.
2. Implement and test R3's authorized, append-only receipt invalidation and
   native reconciliation. Preserve the immutable receipt and block stale
   activation; unresolved mutations must retain ownership. This remains code
   work, not merely missing hardware evidence.
3. Resolve the app activation failure, qualify portable service/spare capacity,
   and establish independent forwarding/load measurements before disruption.
4. Freeze/deploy one candidate; run the six separate prepare/hold/approve/
   activate sequences, both directions for every target, with continuous
   app/path probes, PDB negatives, restart recovery and final settlement.
5. Require candidate-head CI and reviewer approval. Wider E08/E11/E12
   prerequisites remain tracked in the execution plan and are not implied
   passes or silently removed from the complete roadmap.
