# Catalyst Center merge qualification — 9 October 2026

This is a merge-readiness checkpoint, not a completed unattended SWIM qualification.
The subsequent [completed automatic-cycle record](../merge-cycle/README.md)
documents the fixes, uninterrupted version-changing qualification and remaining
repository merge gates. This earlier checkpoint is retained as historical evidence.

## Baseline and scope

- Current `main`: `d4a6a91fe9bafaf9faa0ba037a9096be97779994`.
- Modular controller prerequisite: `pr/moskrive/nd-support`,
  `aea45af1dc3905d1094fe19d098bb8bf618cdb43`, open PR #203.
- Catalyst Center is stacked on that prerequisite. Its adapter implementation
  does not modify the Nexus Dashboard adapter relative to that base.
- Existing Direct/gNOI execution remains the default. Catalyst Center SWIM and
  retired-archive preparation require explicit source/policy selection.

## Changes during merge review

- The preparation example now reserves 2.8 GB for incoming archive plus extracted
  packages, separately from the unchanged 3,070,230,528-byte activation floor.
  These are example bounds requiring image/platform qualification. A policy is
  immutable and cannot be edited underneath an approved campaign.
- Current documentation now distinguishes implemented preparation, historical
  proposals, supervised cleanup and genuinely automatic execution evidence.
- SSH cancellation/deadlines now cover authentication as well as command
  execution. A silent SSH peer cannot strand a claimed cleanup operation before
  the command is sent. The stalled-peer regression checks cancellation and timeout.
- CRDs, deep-copy methods and controller RBAC were regenerated. The controller
  retains read-only access to the handoff journal; controller and device workers
  retain separate ownership of their status and mutation authority.

Source/chart digest (sorted repository-relative Go and chart/CRD paths, each
followed by NUL, file bytes and NUL):
`8cb79941d3cb66d99bc00908cbb9cf25638ed0b1c6e6bc60c35f533021d3774f`.

## Local validation

- Full uncached race regression: `go test -race -count=1 ./...` passed.
- `go vet ./...` and `go build ./...` passed.
- All controller tests with the `envtest` build tag passed, including SWIM
  admission and the rollout leaf API round-trip, using Kubernetes 1.35.0.
- Helm lint passed; Python lab-runner/acceptance safety tests passed.
- Generated provider parity and configuration-documentation checks passed.
- The final SSH transport passed the live read-only lab regression on `.101`,
  including trusted host-key verification, valid show commands and recovery from
  an intentionally invalid show command. No configuration or image was changed.
- The exact CI envtest package set passed: provider, controller, topology rollout
  and Nexus Dashboard. Local tests are not remote CI results.

## Live qualification blocker

Both `.101` and `.103` are Ready and have no scheduled Kubernetes Pods or native
applications. `.101` is committed on 26.02.01 with 3,108,225,024 bytes free.
`.103` is committed on 17.18.03, with 17.18.02 still installed inactive, and
2,504,589,312 bytes free. Neither meets the example 5,870,230,528-byte initial
floor. The corrected reserve is deliberately not reduced just to pass this gate.

`.103` also has multiple historical receipts for its old image, including a
non-invalidated preparation. Installed/referenced packages are not eligible
for the retired-archive cleanup policy. `.101`'s previously qualified retired
17.18.02 archive was already removed; no new eligible cleanup fixture was
restored during this checkpoint. Lab rebaselining must preserve committed,
boot, rollback and valid preparation references rather than deleting status
records or bypassing admission.

No new distribution or activation was submitted during this checkpoint. The
[successful normal-reload run](../standard-reload/README.md) remains the evidence
for the live upgrade; its manual offloads are not unattended-remediation evidence.

## Remaining gates

- Prepare an eligible lab fixture with sufficient bounded reclaimable capacity,
  then complete a version-changing automatic-cleanup campaign on the final build.
- Qualify restart, pause/resume and controller-connectivity recovery on that build
  without duplicate mutations or inappropriate fence release.
- Pass required remote CI for the exact review commit and obtain review approval.
- Merge the ND prerequisite, then retarget/revalidate Catalyst Center against main.

Keep the Catalyst Center review in draft until these gates are closed.

## CI security follow-up

The first GitHub smoke run on `0bfaa9e6` found reachable vulnerabilities in the
previous Go 1.26.7 / x/net v0.58.0 baseline. The fix pins Go 1.26.9 throughout
builds, CI and the installer, and updates x/net to v0.60.0 in both Go modules,
with the dependency graph's required x/crypto, x/sys, x/term and x/text updates.
The installer also rejects Go 1.27 patches below 1.27.2. Official download
checksums and the multi-platform builder digest were verified; historical
release notes and earlier qualification logs retain their original versions.

The patched full race suite and separate Terraform race suite passed. The
controller vulnerability scan reports zero reachable vulnerabilities (it still
reports non-reachable dependency findings); the Terraform scan reports none.
All 16 release-contract tests passed. No advisory was suppressed. See the
[Go advisory](https://pkg.go.dev/vuln/GO-2026-6617) for the fixed version floors.
These security changes require a new CI run and a newly built lab image; prior
live qualification does not certify that new binary.
