# Krew distribution

This directory owns the source template for the `cisco-vk` Krew manifest. Krew
uses its conventional hyphenated vendor name, so Krew users invoke
`kubectl cisco-vk`. The archive keeps the established `kubectl-ciscovk` binary
name; manual/source installs can continue to expose `kubectl ciscovk` as well.

The renderer consumes the signed checksum manifest produced by the release
workflow. It requires the Git tag and manifest version to use the same strict
SemVer-compatible CalVer (for example, `v2026.9.0`, not `v2026.09.0`). Windows
is intentionally absent until the release pipeline has a native Windows
execution gate.

## Release sequence

1. Merge changes through the normal protected PR flow.
2. Before publishing the first plugin-bearing release, enable
   [immutable releases](https://docs.github.com/en/enterprise-cloud@latest/code-security/concepts/supply-chain-security/immutable-releases)
   for this repository. The post-publication workflow fails closed unless the
   individual release reports `immutable: true`.
3. Use a strict-SemVer Git tag for every plugin-bearing release, for example
   `v2026.9.0` rather than `v2026.09.0`. The release and Krew workflows reject
   padded month or patch components because Krew's new-plugin checklist
   requires the Git release tag itself to be semantic.
4. Publish the already-verified GitHub draft. The read-only `krew-index`
   workflow verifies the immutable release, signed checksums, exact archive
   hashes, all four Krew installs, and the native plugin version. It uploads the
   concrete `cisco-vk.yaml` as a workflow artifact.
5. For the first release, submit that artifact manually as
   `plugins/cisco-vk.yaml` in
   [kubernetes-sigs/krew-index](https://github.com/kubernetes-sigs/krew-index).
   Krew requires the initial submission and Kubernetes CLA to be handled by the
   plugin author.
6. After the initial manifest is accepted, subsequent published releases use
   the checksum-pinned `krew-release-bot` binary to open version update PRs.
   The bot receives no repository token or secret, and the CVK workflow has
   read-only contents permission.

Do not retrofit plugin archives onto `v2026.08.0`; that release predates the
archive pipeline and is already public.
