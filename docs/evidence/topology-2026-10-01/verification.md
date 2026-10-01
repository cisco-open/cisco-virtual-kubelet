# Checkpoint verification — 1 October 2026

These tests concern the **saved checkpoint source**, not a newly deployed lab
image. Exact changed-file fingerprints are in
[checkpoint-source.json](checkpoint-source.json); obtain the enclosing commit
from Git. Physical results and limitations are recorded separately in the
[evidence index](README.md). No new Install/Activate was performed for this
archive/commit task.

| Check | Result | Scope / artifact |
| --- | --- | --- |
| `go test -race ./...` | PASS, exit 0; cached results included | [Go race output](verification/go-race.txt); no-test-file package lines filtered for readability |
| `python3 -m unittest discover -s scripts/tests -p 'test_iosxe_gnoi_lab_cycle*.py'` | PASS, 26 tests | [Harness output](verification/harness.txt); late evidence, foreign namespace/UID and key-prefix negatives included |
| `helm lint charts/cisco-virtual-kubelet` | PASS | [Lint output](verification/helm-lint.txt); icon recommendation only |
| Latest archived app log + leaf revalidation | PASS for the two recorded UIDs | [Offline analysis](offline-analysis.json); preceding exact log identity and ordered leaf timestamps/inventory revisions, not traffic qualification |
| Evidence sanitization audit | No remaining PEM/JWT/URI-userinfo/password-assignment match in saved artifacts | Secret/token field review plus textual scan; session tokens hashed. This is not a comprehensive secret-scanner attestation |
| Markdown/site build and whitespace | See final validation output | [Documentation validation](verification/docs.txt) |
| Saved artifact hashes | See final validation output | [Integrity validation](verification/integrity.txt); verify `SHA256SUMS` again after checkout |

Local tools: Go `go1.26.7 darwin/arm64`, Python `3.10.13`, Helm
`v4.1.3+gc94d381`, MkDocs `1.6.1`. They describe this local run, not the final
release toolchain qualification.

Not run/closed by this checkpoint: pinned real-apiserver envtest, two-pass
generated-artifact parity, live bound-token RO/admission and initialization
ordering scenarios, candidate remote CI, optional-version native TAS,
service/forwarding and disruptive fault matrix, independent staged activation,
second platform and scale. Historical passing tests do not fill those gaps.
Run the E00-G/H and package-specific lanes on the next clean candidate.

No PR creation, merge, release tag or production deployment is implied by
committing/pushing this checkpoint. Recheck actual branch CI before proposing
any future merge.

## C0/C1 candidate follow-up — `3212f777`

These results apply to the clean committed candidate, not the earlier
checkpoint build:

| Check | Result |
| --- | --- |
| `go test -race -count=1 ./...` | PASS |
| Pinned Kubernetes 1.35 `make test-envtest` | PASS; provider and controller real-API suites |
| `helm lint` and topology render contract | PASS |
| Strict MkDocs build and exact license closure in an isolated hashed environment | PASS |
| Kubernetes 1.35 managed shared-worker/bound-token integration | PASS |
| Kubernetes 1.35 native topology scheduler/admission integration in a dedicated disposable cluster | PASS |
| Evidence `shasum -a 256 -c SHA256SUMS` before this follow-up update | PASS |
| Physical `.103` exact-target uncertainty recovery, soak and fence settlement | PASS; see [C0 audit](c0-103-activation-outcome-audit.md) |

The initial topology integration invocation selected a stale disposable kind
context and stopped at Helm ownership validation before tests ran. It did not
target Ubuntu16. The lane was rerun successfully in a newly named Kubernetes
1.35 cluster, which was deleted by explicit cleanup.

Candidate-specific remote CI, two-pass generated-artifact parity, independent
Install-only qualification, service/path traffic, accepted manager-owned
network evidence and the remaining C2–C9 gates are still open.

## C2 accepted-evidence follow-up — `90bc690c`

These results qualify the manager-owned network-evidence trust boundary. They
do not qualify measured loaded traffic, supervisor redundancy or a disruptive
software transition:

| Check | Result |
| --- | --- |
| `go test -race -count=1 ./...` | PASS |
| Pinned Kubernetes 1.35 `make test-envtest` | PASS |
| Two-pass generated CRD/deep-copy parity | PASS; identical tree digest |
| Helm lint and topology render/embedded-policy contract | PASS |
| Kubernetes 1.35 managed shared-worker/bound-token integration | PASS; a genuine Pod-bound worker token could not forge `acceptedNetwork` |
| Physical deployment identity | PASS; Helm revision 120 and all six C9K app/network workers ran candidate `90bc690c` |
| Exact Pod/device/revision acceptance on `.100`, `.101`, `.103` | PASS; manager accepted current raw samples on all three switches |
| Read-only IOS-XE CLI comparison | PASS for interface state, CDP count/identity and absence of OSPF neighbors |
| Manager restart | PASS; accepted evidence survived and advanced with the same worker Pod bindings |
| Post-convergence health | PASS; Nodes Ready, sessions Settled, mutation Leases empty and no steady-state manager/worker authorization errors |

The acceptance timestamp cannot make old telemetry fresh: freshness is
calculated from the collection start. Rollout gates consume only the accepted
copy. The exact operation UIDs, revisions and physical observations are in the
[C2 evidence record](c2-manager-accepted-network-evidence.md).

C2 remains open for a controlled idle/loaded traffic source with a declared
tolerance, redundant-supervisor hardware/capability coverage, and the reverse
old-manager/new-worker plus explicit rollback compatibility cases. C3
headroom logic must not be qualified from the current rate samples until that
fixture passes.

## C2 rate-provenance compatibility follow-up — `95077ba7`

The full race suite, pinned Kubernetes 1.35 envtest suite, focused
provider/controller/IOS-XE tests, Helm/render checks, deterministic generation
and Kubernetes 1.35 bound-token lane passed. Physical execution on k3s
`v1.35.8+k3s1` exposed an unsafe rounded OpenAPI maximum in the preceding
candidate; `95077ba7` corrects it to JSON-safe `2^53-1` and adds a generated
schema regression test.

Helm revision 122 converged the manager and all six physical C9K workers to
the exact candidate. Raw and accepted samples matched each current producer
Pod and carried 44–46 capacity/directional-rate/source records. Three
read-only interface operations succeeded and the manager-only restart kept
the workers stable while accepted sequences advanced. See the
[rate-provenance record](c2-rate-provenance-followup.md) for exact hashes,
operation UIDs and remaining limitations.

## C3 claim-time network-authority follow-up — `9c48a98c`

The full race suite and pinned Kubernetes 1.35 envtest suite passed after the
manager grant, monotonic renewal and worker claim-time implementation. Native
API tests reject partial tuples and non-increasing renewals. Provider tests
prove that expired or replaced evidence creates no durable mutation claim or
activation marker, while work already accepted remains observable instead of
being replayed.

Helm revision 123 converged the manager and all six physical C9K workers to the
exact candidate. Fresh accepted samples matched the replacement network-worker
Pod UIDs; three secure gNOI `OS.Verify` operations succeeded; all three Nodes
were Ready, schedulable and untainted. The settled three-minute manager/worker
log window contained no errors. See the
[C3 evidence record](c3-claim-time-network-authority.md) for exact artifact
hashes, operation UIDs and the remaining E03 limitations.

## C3 administrator disruption-protection follow-up — `617b1cfc`

The full race suite, pinned Kubernetes 1.35 envtest suite, topology render
contract and strict MkDocs build passed. Tests cover bounded policy validation,
deterministic overlapping matches, order-stable hashes, planning refusal,
pre-execution policy tightening and absent legacy Helm values.

The initial physical `--reuse-values` upgrade found a nil-list template defect
and failed before apply. The repaired exact candidate deployed as Helm revision
124; the manager and all six C9K workers converged to `617b1cfc`, while the
retained policy document continued to omit the empty optional field. All three
Nodes remained Ready, schedulable and untainted; accepted samples matched the
current network-worker Pod UIDs; three secure read-only gNOI Verify operations
succeeded. See the
[administrator-protection record](c3-administrator-disruption-protections.md).

No live protection rule or device mutation was used. Physical redundant,
singleton, critical-service and congested-path cases, overlapping groups,
transfer pacing and continuous recovery/soak enforcement remain open.
