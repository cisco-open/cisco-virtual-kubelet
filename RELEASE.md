# Release runbook

This is the maintainer checklist for the proposed corrected November candidate
**v2026.11.1**, compared with published **v2026.10.0**. The original November
release target date was **1 November 2026**; preparation does not establish
publication. The
[canonical November notes](docs/releases/v2026.11.1.md) define the release scope;
the [preparation record](docs/releases/v2026.11.1-readiness.md) separates local
preflight from the checks required on the final candidate. Preparing or merging
this PR does not publish the release.

Tags use strict SemVer-compatible CalVer: `vYYYY.M.PATCH` (no leading zero in
the month). For example, `v2026.11.1` uses Helm chart version `2026.11.1`.

The release workflow deliberately cannot create or publish a GitHub Release.
A maintainer creates a curated draft **before** pushing the tag, the workflow
stages the signed assets into that draft, and a maintainer publishes it only
after every verification below passes. Published releases are immutable.

`v2026.9.0` and `v2026.9.1` are unpublished, immutable recovery markers. The
`v2026.9.0` workflow stopped in draft-visibility preflight before creating any
artifact. The corrected `v2026.9.1` workflow staged 16 draft assets and pushed
signed image and chart tags, but its release-image scan found fixed
HIGH/CRITICAL dependency findings, so maintainers made a pre-publication no-go
decision. Never move or delete either tag and never publish either draft.
Neither candidate triggered documentation deployment or a Krew update.
`v2026.9.2` was published on 1 September.
`v2026.10.0` was published on 1 October. The
[October readiness ledger](docs/releases/v2026.10.0-readiness.md) is a historical
preparation snapshot, not the checklist for a new candidate.

`v2026.11.0` is a **rejected, unpublished, immutable recovery marker** from
preparation PR #210, merge `089c5c4a2f13e09c2b1ef462da230841184a30f5`.
Exact staged-image inspection found MPL-labelled embedded SD-WAN JSON without
its copied source/attribution bundle, failing the licensing hard gate below.
Never publish its draft, move or delete its tag, or replace its staged artifacts.
Signed tags or a successful staging run do not establish publication readiness.
The [rejected-candidate record](docs/releases/v2026.11.0-readiness.md) preserves
that decision. `v2026.11.1` retains the November scope and adds the packaging
correction; it requires a new reviewed candidate and fresh artifact verification.

Do not rerun the rejected `v2026.11.0` tag workflow. Do not rerun the
`v2026.9.0` or `v2026.9.1` tag workflows. In particular, the
older `v2026.9.1` workflow emitted mutable image aliases before its advisory
scan; a rerun could move those aliases outside the corrected gates below.

## Release approval policy

One normal GitHub **Approve** review on the current release-preparation PR head
from a repository maintainer other than the release operator is sufficient
release authorization. The reviewer must have repository write or admin access.
The review body is optional: no prescribed wording, separate authorization
comment, or post-merge draft approval is required.

The preparation PR must identify the release version, previous published
baseline, committed release notes, release scope, and relevant security,
compatibility, and upgrade limitations. Approval covers creating the signed
tag and publishing that reviewed release once all required checks and the
staging and verification steps below pass. It does not waive a failing gate.

Normal branch protection still applies: approval must be current and
undismissed, outstanding change requests must be resolved, and all required
checks must pass before merge. Changed candidate source, version, scope, or
canonical release notes go through review again. The different SHA produced
by the protected merge does not by itself require another approval.

The release operator records the preparation PR and approval links, approved
PR head SHA, resulting merge SHA, draft ID, and verification evidence in the
release issue or PR record. This bookkeeping is not another reviewer task;
do not commit it back into the frozen source and create a new review cycle.
Creating the draft from the reviewed notes and recording its ID require no
additional approval. The operator verifies the draft and artifacts before
publication, using the approved release-signing identity and the gates below.

## 1. Freeze the release commit

- [ ] Open a pull request to `main`; do not release directly from a topic
  branch. Prepare the canonical release notes, version selectors, upgrade
  instructions, and scope/limitations in this PR before requesting approval.
- [ ] Review the complete change set from the previous release:
  `git log --oneline v2026.10.0..HEAD` and
  `git diff --stat v2026.10.0..HEAD`.
- [ ] Confirm the release-facing version and source chart `appVersion` are
  `v2026.11.1`. The release workflow must stamp the packaged chart version as
  `2026.11.1`; source `Chart.yaml` intentionally retains its local-development
  chart version. Historical examples must retain their original version.
- [ ] Regenerate committed material and require a clean diff:

  ```sh
  make crd-gen deepcopy-gen rbac-gen helm-sync-crds
  make parity-matrix
  make config-docs
  git diff --exit-code
  ```

- [ ] Run the root Go tests with the pinned release toolchain (`go1.26.10`),
  `go vet ./...`, `go test -race ./...`,
  `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...`, the same exact
  vulnerability scan from `tools/terraform-provider-iosxeconfig`, and the hard
  Trivy HIGH/CRITICAL scan of the built controller image; Helm 3 and Helm 4
  lint/template/install checks, Krew packaging tests, workflow `actionlint`,
  the hashed MkDocs install and `mkdocs build --strict`, the website license,
  lint, build, and `npm audit --audit-level=high` gates. Use separately
  downloaded, checksum-verified Helm `v3.21.4` and `v4.2.4` binaries for the
  Helm checks; an ambient `helm` is not release evidence. These documentation
  checks must pass in the protected pre-merge `build-and-smoke` job; the
  post-publication deployment is verification, not the first build attempt.
- [ ] Require the normal approval described above and every protected `main` check:
  `build-and-smoke`, `ygot-validate`, `terraform-provider`, `govulncheck`,
  `helm4-compat`, `lab-ci-next / cat8kv`, and `lab-ci-next / cat9k`.
- [ ] Re-run both hardware/lab checks on the final commit if the pull request
  changed after they passed. Inspect their logs, not only the aggregate state.
- [ ] Record the approval link and approved PR head, then merge the pull request.
  Set `release_pr` to that preparation PR's number. Select its recorded merge
  commit on `main` so later unrelated changes are not added to the candidate:

  ```sh
  set -euo pipefail
  release_pr=REPLACE_WITH_RELEASE_PR_NUMBER
  git fetch origin main --tags
  release_commit="$(gh pr view "$release_pr" \
    --repo cisco-open/cisco-virtual-kubelet \
    --json state,baseRefName,mergeCommit \
    --jq 'select(.state == "MERGED" and .baseRefName == "main") | .mergeCommit.oid')"
  test "$release_commit" = "$(git rev-parse "${release_commit}^{commit}")"
  git merge-base --is-ancestor "$release_commit" origin/main
  test -z "$(git status --porcelain=v1 --untracked-files=all)"
  git switch --detach "$release_commit"
  test "$(git rev-parse HEAD)" = "$release_commit"
  git diff --quiet
  git diff --cached --quiet
  ```

  Main may advance without changing this frozen candidate. Including those
  later changes requires a new release-preparation PR and normal approval.

- [ ] Treat `release_commit` as immutable from this point forward. Every draft,
  tag, workflow run, artifact, and verification record must name that full
  commit SHA. Any source, generated-file, release-workflow, or documentation
  change requires a new protected pull request and a complete restart of this
  section.

### Approve the release workstation and pinned tools

- [ ] Before creating the draft, use a release workstation approved by the
  project and confirm its configured tag-signing identity, key fingerprint,
  signing backend, and access controls against the maintainer record. A key
  that can merely produce a signature is not sufficient approval.
- [ ] Independently download and checksum-verify Cosign `v3.1.3`, Helm
  `v3.21.4`, and Helm `v4.2.4` from their official releases. Do not use the
  executables or downloaded artifacts produced by the release workflow to
  verify that same workflow. Set explicit absolute paths and reject an
  unexpected version:

  ```sh
  helm3=/absolute/path/to/helm-v3.21.4
  helm4=/absolute/path/to/helm-v4.2.4
  cosign=/absolute/path/to/cosign-v3.1.3

  test "$("$helm3" version --short | cut -d+ -f1)" = v3.21.4
  test "$("$helm4" version --short | cut -d+ -f1)" = v4.2.4
  "$cosign" version --json | jq -e '.gitVersion == "v3.1.3"' >/dev/null

  for helm_binary in "$helm3" "$helm4"; do
    "$helm_binary" lint charts/cisco-virtual-kubelet
    "$helm_binary" template cvk charts/cisco-virtual-kubelet \
      --namespace cvk-system >/dev/null
  done
  ```

- [ ] Prove the approved tag-signing configuration works against the immutable
  commit before the real draft or tag exists. The probe is local only and must
  never be pushed:

  ```sh
  signing_key="$(git config --get user.signingkey)"
  test -n "$signing_key"
  signing_probe="cvk-signing-probe-$(printf '%.12s' "$release_commit")"
  test -z "$(git tag --list "$signing_probe")"
  trap 'git tag -d "$signing_probe" >/dev/null 2>&1 || true' EXIT
  git tag -s "$signing_probe" "$release_commit" \
    -m "Cisco Virtual Kubelet release signing probe"
  git verify-tag "$signing_probe"
  git tag -d "$signing_probe"
  trap - EXIT
  ```

### Licensing is a hard gate

- [ ] Confirm the root and packaged-chart Apache licenses are byte-identical,
  and inspect the per-binary third-party license bundles. They must contain the
  Go toolchain license and patents, all applicable dependency license text and
  upstream NOTICE files, and the source copied for MPL-2.0-covered files.
- [ ] Inspect `/licenses/embedded-source/` in the exact staged controller and
  config-lint images. Compare `MANIFEST.json` entries and the copied
  `source/<repository-relative path>` files byte-for-byte against the frozen
  source: SD-WAN embedded definitions, their LICENSE/README, upstream lock and
  generators; linked IOS-XE model/schema sources, provenance and applicable
  Cisco attribution. Confirm each target's linked embedded inputs are covered.
  The build-time helper and its tests supplement, not replace, this inspection.
- [ ] Confirm the website and MkDocs static outputs contain their committed,
  generated third-party bundles. `docs/MKDOCS_THIRD_PARTY_LICENSES.txt` must
  exactly cover the hashed `requirements.txt` closure, including Material for
  MkDocs, and the source-map-derived Mermaid/ResizeObserver browser-runtime
  closure. Run `make mkdocs-build` to verify the deployed runtime bytes and
  remote-resource gate. The website output must contain `LICENSE`, `NOTICE`,
  and `THIRD_PARTY_LICENSES.txt`.
- [ ] Review the image and plugin SBOMs as inventories; an SBOM is not a
  substitute for redistributing license and NOTICE text.
- [ ] Confirm that the generated IOS-XE schema and YANG material match their
  recorded upstream provenance. The current generated files embed data derived
  from Cisco YANG models at YangModels/yang commit
  `647250f1c8f59aaf1ecdcf1d908fde96c036b1ba`, whose upstream terms are the
  [Cisco API License 1.1](https://github.com/YangModels/yang/blob/647250f1c8f59aaf1ecdcf1d908fde96c036b1ba/vendor/cisco/xe/LICENSE.md)
  and whose modules carry Cisco copyright notices. Before tagging, verify that
  any changes to upstream terms, provenance source, model scope, or distributed
  artifact types are reflected in the applicable license and notice bundles.

## 2. Verify the approved curated notes

- [ ] Verify the already reviewed, committed
  `docs/releases/v2026.11.1.md` notes from the final comparison
  `v2026.10.0...<release_commit>`. Confirm they cover user-visible features,
  compatibility and security changes, known limitations, upgrade steps, and
  links to the documentation. If corrections are needed, return to section 1
  for review before freezing a new commit. Use the approved committed file as
  the canonical source. Render its
  site-relative links as tag-pinned GitHub links with the checked-in renderer
  before creating the curated GitHub draft; never edit the canonical MkDocs
  source just for GitHub.
- [ ] Confirm the notes distinguish October's existing managed topology and
  combined rollouts from November's staged activation, network-evidence and
  controller-adapter additions. Review the `CiscoDevice`, `NetworkController`,
  `IOSXESoftwareRollout` and `IOSXESoftwareUpgrade` schema changes and the full
  generated CRD diff. Use the complete chart CRD upgrade procedure below;
  November does not introduce a new CRD kind.
- [ ] Do not claim the release is available until the draft has been published.

## 3. Create the draft before the tag

Set these in a clean maintainer shell:

```sh
release_version=v2026.11.1
release_name="Cisco Virtual Kubelet ${release_version}"
repo=cisco-open/cisco-virtual-kubelet
repo_root="$(git rev-parse --show-toplevel)"
release_notes_source="${repo_root}/docs/releases/v2026.11.1.md"
release_notes="$(mktemp)"
test -s "$release_notes_source"
python3 "${repo_root}/.github/scripts/render_github_release_notes.py" \
  --source "$release_notes_source" \
  --output "$release_notes" \
  --repo-root "$repo_root" \
  --tag "$release_version" \
  --repository "$repo"
test -s "$release_notes"
test -z "$(grep -E '\]\((\.\.?/|/)' "$release_notes" || true)"
test "$(git rev-parse HEAD)" = "$release_commit"
git merge-base --is-ancestor "$release_commit" origin/main
test -z "$(git status --porcelain=v1 --untracked-files=all)"
```

- [ ] Confirm that neither the tag nor a release record exists. Any result is
  an abort condition; never overwrite, delete, or move an existing release
  tag.

  ```sh
  test -z "$(git ls-remote --tags origin "refs/tags/${release_version}" "refs/tags/${release_version}^{}")"
  release_inventory="$(mktemp)"
  gh api --paginate --slurp \
    "repos/${repo}/releases?per_page=100" > "$release_inventory"
  test "$(jq --arg tag "$release_version" \
    '[.[][] | select(.tag_name == $tag)] | length' \
    "$release_inventory")" -eq 0
  rm -f "$release_inventory"
  ```

- [ ] Create exactly one non-prerelease draft, targeting the recorded commit.
  Keep the returned release ID.

  ```sh
  payload="$(mktemp)"
  jq -n \
    --arg tag "$release_version" \
    --arg target "$release_commit" \
    --arg name "$release_name" \
    --rawfile body "$release_notes" \
    '{tag_name:$tag,target_commitish:$target,name:$name,body:$body,draft:true,prerelease:false}' \
    > "$payload"
  release_id="$(gh api --method POST "repos/${repo}/releases" \
    --input "$payload" --jq .id)"
  gh api "repos/${repo}/releases/${release_id}" \
    | jq -e --arg tag "$release_version" --arg target "$release_commit" '
        .tag_name == $tag and .target_commitish == $target and
        .draft == true and .prerelease == false and .published_at == null'
  rm -f "$payload" "$release_notes"
  ```

- [ ] As the release operator, verify that the rendered draft matches the
  reviewed canonical notes, including scope, security/compatibility limits,
  and upgrade warnings. Confirm the tag-pinned links render correctly, the
  non-prerelease draft targets the recorded merge commit, and it has zero
  assets before the tag is pushed. Record its ID alongside the existing PR
  approval and commit evidence. That normal approval is the release
  authorization; continue without requesting another reviewer comment.
- [ ] Correct any draft mismatch against the reviewed source before tagging.
  If the candidate or canonical notes need to change, return to the protected
  PR process instead of editing new release scope into the draft.

## 4. Push the tag and let automation stage the draft

Only after the draft matches the approved preparation and the checks above pass:

```sh
test "$(git rev-parse HEAD)" = "$release_commit"
git merge-base --is-ancestor "$release_commit" origin/main
test -z "$(git status --porcelain=v1 --untracked-files=all)"
git tag -s "$release_version" "$release_commit" \
  -m "Cisco Virtual Kubelet ${release_version}"
test "$(git rev-list -n 1 "$release_version")" = "$release_commit"
git verify-tag "$release_version"
git push origin "refs/tags/${release_version}"
```

- [ ] Confirm the `release` workflow resolved the tag to the exact full commit
  SHA and that every job succeeded. The workflow must leave the release in
  draft state.
- [ ] Confirm the image was built with Go 1.26.10 and reports the exact tag,
  full SHA, and RFC3339 UTC commit build time from both `version` and
  `--version`.
- [ ] Confirm all four plugin archives execute on their native
  OS/architecture and through the generated Krew manifest.
- [ ] Confirm the draft contains exactly 16 allowlisted assets: four archives,
  four per-platform CycloneDX SBOMs, four archive Sigstore bundles, the
  checksum manifest and its Sigstore bundle, the GitHub provenance bundle, and
  the image CycloneDX SBOM. Verify every recorded size and SHA-256 digest.
- [ ] With the independently installed Cosign `v3.1.3` binary assigned to
  `$cosign` above, verify the image digest's keyless signature and CycloneDX
  attestation, and verify every plugin `sign-blob` bundle and GitHub provenance
  subject. Pin the expected GitHub Actions OIDC issuer and this repository's
  release-workflow certificate identity; do not use unconstrained keyless
  verification.
- [ ] Pull Helm OCI chart `2026.11.1` by digest, verify its cosign signature,
  inspect `Chart.yaml`, and confirm its package contains `LICENSE` identical to
  the repository root.
- [ ] Require both the pre-merge and exact tagged-image Trivy scans to report
  zero fixed HIGH/CRITICAL findings. The tagged-image scan is a hard gate even
  though the image has already been pushed and signed. The root and Terraform
  `govulncheck` gates must also be green.
- [ ] Confirm the mutable `2026.11` and `latest` image aliases were promoted
  only after both tagged-image platform scans passed and all chart, plugin, and
  16-asset draft staging completed. Both aliases must resolve to the exact
  signed image digest. During recovery, treat aliases that still resolve to an
  unpublished candidate as quarantined and do not publish until this check
  passes.

Do not publish a partial draft. If any job or verification fails, follow the
abort rules below.

## 5. Helm upgrade: apply all reviewed November CRDs first

Helm installs files under `crds/` on a fresh installation but does not
update them during `helm upgrade`. Compared with October, November changes
existing schemas and managed runtime contracts; it does not add a new CRD
kind. Review the full directory, especially `CiscoDevice`, `NetworkController`,
`IOSXESoftwareRollout` and `IOSXESoftwareUpgrade`. The directory also includes
`networkcontrollers.cisco.vk`, `networkcontrollerconfigs.config.cisco.vk` and
all configuration/operations schemas required by older installations.

Before the maintenance window, back up schemas, custom resources and Helm
values. Review stored-object compatibility and unresolved campaigns, prepared
receipts and mutation ownership. Do not overlap an installation migration with
disruptive device work. Keep chart, CRDs, admission policies, manager and workers
matched. The [November migration notes](docs/releases/v2026.11.1.md#upgrade-from-october)
and [installation guide](docs/installation-maintenance.md) define the operator path.

After publication, use the complete pinned chart CRD directory:

```sh
helm pull oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.11.1 --untar
kubectl get customresourcedefinitions.apiextensions.k8s.io -o yaml \
  > cvk-crds-before-upgrade.yaml
# Save custom-resource instances and Helm values separately; schemas are not data.
# Diff exit 1 means differences; review before explicit field ownership transfer.
kubectl diff --server-side --force-conflicts --field-manager=cvk-crd-upgrade \
  -f cisco-virtual-kubelet/crds/
kubectl apply --server-side --force-conflicts --field-manager=cvk-crd-upgrade \
  -f cisco-virtual-kubelet/crds/
kubectl wait --for=condition=Established --timeout=60s \
  -f cisco-virtual-kubelet/crds/

helm upgrade <release> \
  oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.11.1 --namespace <namespace> \
  -f <reviewed-values-file> --wait --timeout 5m
kubectl rollout status deployment/<controller-deployment> \
  -n <namespace> --timeout=5m
```

Replace placeholders; use a clean extraction directory and verify the chart
signature. `--force-conflicts` must target only reviewed CVK CRDs. Preserve the
previous gate settings until migration/admission checks pass. The first
combined-to-split managed worker transition requires the documented live Helm
upgrade and identity handoff; do not substitute GitOps pruning or rename retained
policy, namespace, ledger or Lease identities. Verify device and workload state
as well as manager readiness.

The [installation guide](docs/installation-maintenance.md#roll-back-a-release)
covers schema and data rollback risks. A chart rollback does not undo OS
activation or safely discard staged receipts. Never delete CRDs, active operation
records, claims or Leases as a rollback shortcut; follow
[managed recovery and retirement](docs/topology-recovery.md).

## 6. Publish and verify distribution

- [ ] After all verification passes, the release operator publishes the draft
  in the GitHub UI under the existing PR approval, without changing its tag or
  assets. Confirm the release becomes immutable. No further signoff is needed.
- [ ] The documentation deployment must start from the `release: published`
  event, check out the exact release tag, pass strict MkDocs/website/audit and
  license-artifact gates, and publish that version only after the release is
  public.
- [ ] Verify the public release page, image digest, OCI chart, docs site, and
  checksums from an unauthenticated session.
- [ ] The public Krew index already contains `cisco-vk`; do not open an initial
  submission. The `krew-index` workflow should open the normal update-bot pull
  request for `v2026.11.1`. Wait for it to merge, verify the official index has
  the four expected URLs and SHA-256 values, then run `kubectl krew update`,
  install/update `cisco-vk`, and execute `kubectl cisco-vk version`.
- [ ] Exercise a fresh Helm install and the upgrade procedure above in a clean
  cluster. Record the image/chart digests and verification evidence in the
  release issue.

## Abort, recovery, and rollback

- **Before the tag is pushed:** stop, correct the pull request/notes, and delete
  only the known draft by its numeric ID after a second maintainer confirms it
  has no assets and is still a draft. Then restart at section 1.
- **After the tag is pushed:** do not delete, force-push, or move the tag. Image
  and chart tags may already be public and signed. Leave the draft unpublished,
  diagnose the failed workflow, and use a new patch version (`v2026.11.2`) for
  any source or artifact change.
- **After publication:** the release and its assets are immutable. Never replace
  an asset or retag a commit; publish corrective notes if documentation alone
  is wrong, or cut `v2026.11.2` for code/artifact changes.
- **Krew update failure:** an infrastructure-only failure may be rerun. A wrong
  URL, digest, platform, archive layout, or binary requires a new patch release.
- **Helm rollback:** rolling the Deployment/chart back does not remove CRDs or
  stored custom resources. Back up custom resources first, verify schema/storage
  compatibility, and never delete CRDs as a routine rollback step.
