# Release runbook

This is the maintainer checklist for a Cisco Virtual Kubelet release. The next
planned release is `v2026.10.0` on 2026-10-01; its Helm chart version is
`2026.10.0`. Tags use strict SemVer-compatible CalVer:
`vYYYY.M.PATCH` (no leading zero in the month).

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
`v2026.9.2` was published on 1 September and is the October comparison baseline.
The October candidate is still unpublished; follow the
[October readiness ledger](docs/releases/v2026.10.0-readiness.md) before tagging.

Do not rerun the `v2026.9.0` or `v2026.9.1` tag workflows. In particular, the
older `v2026.9.1` workflow emitted mutable image aliases before its advisory
scan; a rerun could move those aliases outside the corrected gates below.

## 1. Freeze the release commit

- [ ] Open a pull request to `main`; do not release directly from a topic
  branch.
- [ ] Review the complete change set from the previous release:
  `git log --oneline v2026.9.2..HEAD` and
  `git diff --stat v2026.9.2..HEAD`.
- [ ] Confirm the release-facing version and source chart `appVersion` are
  `v2026.10.0`. The release workflow must stamp the packaged chart version as
  `2026.10.0`; source `Chart.yaml` intentionally retains its local-development
  chart version. Historical examples must retain their original version.
- [ ] Regenerate committed material and require a clean diff:

  ```sh
  make crd-gen deepcopy-gen rbac-gen helm-sync-crds
  make parity-matrix
  make config-docs
  git diff --exit-code
  ```

- [ ] Run the root Go tests with the pinned release toolchain (`go1.26.7`),
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
- [ ] Require review approval and every protected `main` check:
  `build-and-smoke`, `ygot-validate`, `terraform-provider`, `govulncheck`,
  `helm4-compat`, `lab-ci-next / cat8kv`, and `lab-ci-next / cat9k`.
- [ ] Re-run both hardware/lab checks on the final commit if the pull request
  changed after they passed. Inspect their logs, not only the aggregate state.
- [ ] Merge the pull request and record the exact commit:

  ```sh
  git fetch origin main --tags
  git switch main
  git pull --ff-only origin main
  release_commit="$(git rev-parse HEAD)"
  test "$release_commit" = "$(git rev-parse origin/main)"
  test "$release_commit" = "$(git rev-parse "${release_commit}^{commit}")"
  test -z "$(git status --porcelain=v1 --untracked-files=all)"
  git diff --quiet
  git diff --cached --quiet
  ```

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

## 2. Prepare curated notes

- [ ] Review and update the committed
  `docs/releases/v2026.10.0.md` notes from the final comparison
  `v2026.9.2...<release_commit>`. Include user-visible features, compatibility
  and security changes, known limitations, upgrade steps, and links to the
  documentation. Use that committed file as the canonical source. Render its
  site-relative links as tag-pinned GitHub links with the checked-in renderer
  before creating the curated GitHub draft; never edit the canonical MkDocs
  source just for GitHub.
- [ ] Call out the new `IOSXESoftwareRollout` CRD and the changed
  `CiscoDevice`, `IOSXESoftwareUpgrade` and `IOSXEOperationalAction` schemas;
  use the complete chart CRD upgrade procedure below.
- [ ] Do not claim the release is available until the draft has been published.

## 3. Create the draft before the tag

Set these in a clean maintainer shell:

```sh
release_version=v2026.10.0
release_name="Cisco Virtual Kubelet ${release_version}"
repo=cisco-open/cisco-virtual-kubelet
repo_root="$(git rev-parse --show-toplevel)"
release_notes_source="${repo_root}/docs/releases/v2026.10.0.md"
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
test "$(git rev-parse origin/main)" = "$release_commit"
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

- [ ] Have a second repository maintainer who is not the release operator
  review the rendered draft, exact target commit, version, recovery/security
  delta, preview rollout/drain limitations, and CRD upgrade warning. Record the approval
  as a comment on the release PR; a review approval by itself is not the
  release authorization. The approver must replace both angle-bracket values
  and post this exact text:

  ```text
  I am a Cisco Virtual Kubelet repository maintainer other than the release operator. I approve Cisco Virtual Kubelet v2026.10.0 for release from commit <full 40-character release commit> using draft release ID <draft release ID>.

  I verified that the draft is a non-prerelease draft targeting that exact commit, has zero assets before the tag is pushed, and renders the tag-pinned release-note links correctly. I reviewed the complete delta from v2026.9.2, including secure IOS-XE gNOI authentication/provisioning, native topology projection, topology-aware image selection and combined rollouts, opt-in preview PDB drain, dependency updates, and the hard Linux AMD64/ARM64 HIGH/CRITICAL image gates. I verified the October readiness ledger and exact-candidate lab results. I understand that native TAS is an optional experimental lane, independent staging/activation and the November roadmap are excluded, no zero-downtime guarantee is made, and existing Helm installations must apply all reviewed October CRDs before upgrading.

  I authorize the release operator to create and push the immutable signed v2026.10.0 tag with the approved release-signing identity. I do not authorize publishing v2026.9.0 or v2026.9.1, moving or deleting any existing release tag, rerunning either old tag workflow, or publishing v2026.10.0 until all required checks pass, the draft contains exactly 16 verified assets, both mutable image aliases resolve to the signed v2026.10.0 digest, and the final post-stage verification is complete.
  ```

## 4. Push the tag and let automation stage the draft

Only after the reviewed draft exists:

```sh
test "$(git rev-parse HEAD)" = "$release_commit"
test "$(git rev-parse origin/main)" = "$release_commit"
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
- [ ] Confirm the image was built with Go 1.26.7 and reports the exact tag,
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
- [ ] Pull Helm OCI chart `2026.10.0` by digest, verify its cosign signature,
  inspect `Chart.yaml`, and confirm its package contains `LICENSE` identical to
  the repository root.
- [ ] Require both the pre-merge and exact tagged-image Trivy scans to report
  zero fixed HIGH/CRITICAL findings. The tagged-image scan is a hard gate even
  though the image has already been pushed and signed. The root and Terraform
  `govulncheck` gates must also be green.
- [ ] Confirm the mutable `2026.10` and `latest` image aliases were promoted
  only after both tagged-image platform scans passed and all chart, plugin, and
  16-asset draft staging completed. Both aliases must resolve to the exact
  signed image digest. During recovery, treat aliases that still resolve to an
  unpublished candidate as quarantined and do not publish until this check
  passes.

Do not publish a partial draft. If any job or verification fails, follow the
abort rules below.

## 5. Helm upgrade: apply all reviewed October CRDs first

Helm installs files under `crds/` on a fresh installation but does not
update them during `helm upgrade`. The September-to-October delta adds
`IOSXESoftwareRollout` and changes `CiscoDevice`, `IOSXESoftwareUpgrade`
and `IOSXEOperationalAction`. Applying only September's two controller CRDs
is insufficient. Back up custom resources as well as schemas.
The full directory also retains September's `networkcontrollers.cisco.vk`
and `networkcontrollerconfigs.config.cisco.vk` for upgrades from older releases.

After publication, use the complete pinned chart CRD directory:

```sh
helm pull oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.10.0 --untar
kubectl get customresourcedefinitions.apiextensions.k8s.io -o yaml \
  > cvk-crds-before-upgrade.yaml
# Diff exit 1 means differences; review before explicit field ownership transfer.
kubectl diff --server-side --force-conflicts --field-manager=cvk-crd-upgrade \
  -f cisco-virtual-kubelet/crds/
kubectl apply --server-side --force-conflicts --field-manager=cvk-crd-upgrade \
  -f cisco-virtual-kubelet/crds/
kubectl wait --for=condition=Established --timeout=60s \
  crd/ciscodevices.cisco.vk \
  crd/iosxesoftwareupgrades.ops.cisco.vk \
  crd/iosxeoperationalactions.ops.cisco.vk \
  crd/iosxesoftwarerollouts.ops.cisco.vk

helm upgrade <release> \
  oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.10.0 --namespace <namespace> \
  -f <reviewed-values-file> --wait --timeout 5m
kubectl rollout status deployment/<controller-deployment> \
  -n <namespace> --timeout=5m
```

Replace placeholders; use a clean extraction directory and verify the chart
signature. `--force-conflicts` must target only reviewed CVK CRDs. Keep
topology, software mutation and drain gates at their previous settings until
the migration/admission tests pass; do not blindly reuse unqualified flags.
The [operations runbook](docs/operations.md#upgrading-crds) covers schema and
data rollback risks. Never delete CRDs or active operation records as rollback.

## 6. Publish and verify distribution

- [ ] In the GitHub UI, publish the reviewed draft without changing its tag or
  assets. Confirm the release becomes immutable.
- [ ] The documentation deployment must start from the `release: published`
  event, check out the exact release tag, pass strict MkDocs/website/audit and
  license-artifact gates, and publish that version only after the release is
  public.
- [ ] Verify the public release page, image digest, OCI chart, docs site, and
  checksums from an unauthenticated session.
- [ ] The public Krew index already contains `cisco-vk`; do not open an initial
  submission. The `krew-index` workflow should open the normal update-bot pull
  request for `v2026.10.0`. Wait for it to merge, verify the official index has
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
  diagnose the failed workflow, and use a new patch version (`v2026.10.1`) for
  any source or artifact change.
- **After publication:** the release and its assets are immutable. Never replace
  an asset or retag a commit; publish corrective notes if documentation alone
  is wrong, or cut `v2026.10.1` for code/artifact changes.
- **Krew update failure:** an infrastructure-only failure may be rerun. A wrong
  URL, digest, platform, archive layout, or binary requires a new patch release.
- **Helm rollback:** rolling the Deployment/chart back does not remove CRDs or
  stored custom resources. Back up custom resources first, verify schema/storage
  compatibility, and never delete CRDs as a routine rollback step.
