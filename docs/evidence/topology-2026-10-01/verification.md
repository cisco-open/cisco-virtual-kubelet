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
