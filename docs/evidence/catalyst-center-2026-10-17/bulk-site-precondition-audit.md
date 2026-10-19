# Bulk-site provisioning validation coverage audit

The pinned NetAsCode 0.5.0 `cc_device_provision.tf` declares
`terraform_data.bulk_site_provisioning_validation`: when bulk execution has an
explicit root, that root must exist in the configured YAML site set.

CVK's existing `configuredWiredBulkBatches` loads the complete source site
context and `groupWiredBulkMembers` rejects an undeclared explicit root before
building a batch. The existing option parser validates the bulk switch, root,
managed-site selection, exact-selection and global-settings options. Additional
bounded grouping restrictions remain CVK execution limits; they are not claimed
to reproduce every upstream grouping combination.

The [passing audit](bulk-site-precondition-audit.log) reruns the grouping,
site-selection, module-option/source-grouping and public no-fallback families
under the race detector (3.279 s). Public refusal cases use fake Kubernetes
storage and a native-call counter: unsupported/invalid bulk declarations produce
the expected failure condition, zero native calls and no operation, owner or
application receipt. They cannot fall through to singular device provisioning.
Case counts, current source hashes and the pinned upstream input hash are in
[source evidence](bulk-site-precondition-audit-sources.json).

This corrects the ledger's stale claim that the bulk-root validation itself was
missing. It adds no execution authority. Public bulk dispatch, installed group
actors, independent per-device convergence/settlement and physical qualification
remain incomplete. No lab device mutation was performed by this audit.
