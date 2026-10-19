# Same-owner SNMPv3 metadata promotion

This change closes the receipt-family boundary for promoting a CVK-owned global
NOAUTHNOPRIV definition to authenticated SNMPv3. It uses the existing configuration
journal, transport, projected versioned secrets and task readers. There are no
new CRD fields, transport clients or provider paths; ND and direct upgrade logic
are unchanged by this change.

Initial promotion requires the original metadata action receipt, unchanged
non-secret NOAUTH payload and exact native UUID. It issues a PUT, never an
adopting or replacement POST. The metadata receipt stays unchanged as origin
proof; successful promotion is recorded in the ordinary versioned receipt.
Later privacy enablement and password rotation reuse that receipt and the
existing counter rules. Downgrading to NOAUTH or dropping a previously delivered
secret destination remains refused.

Original accepted/Unknown recovery validates the owner/config, target UUID,
projected versions, original task identity, source scope and origin chronology.
The first authenticated receipt cannot be published if its metadata origin is
missing, duplicated, foreign, replaced, from a future generation, or reused as
the new task. The existing final status CAS remains the persistence boundary.
The common frozen whole-selection and one-use submission guard is retained.

## Qualification

The native public-reconciler run created only one generated, unassigned definition,
`CVK-SNMP-Recover-52722922545193a5`. It exercised NOAUTHNOPRIV creation,
AUTHNOPRIV promotion, AUTHPRIV privacy addition and authentication counter 1→2.
Four original tasks recovered from Unknown with no replay; both receipt families
and the same native UUID were verified. The generated UUID
`5495425e-bdd3-4fce-b8a6-4f83cae5e812` was deleted once, cleanup task
`01a153a3-3d14-7cf8-a5a2-b9258c0676fe` completed, and the complete non-secret
credential inventory returned to its original baseline. No site assignment,
discovery, SNMP endpoint access or device mutation was performed.

The native log is [retained here](snmpv3-metadata-promotion-native.log).
The [machine-readable evidence](snmpv3-metadata-promotion.json) records the
original tasks, source fingerprints, gate results and explicit limitations.
Versioned values and private projections are excluded from repository evidence.
The new `examples/configs/catalyst-center/snmpv3-promotion-apply.yaml` example
is a revision of the existing NOAUTH config, not a competing section owner.

Real temporary Kubernetes API persistence and the native run are separate gates;
the native driver uses simulated Kubernetes actors/storage. Additional boundary
checks cover first-promotion planning and fresh receipt publication after loss
or corruption of the metadata origin. Installed transition behavior, actual
switch authentication, downgrades, destination removal, recreation, adoption,
prune and the other resource-parity gaps remain open. This is not a full-model
completion claim.

A second native public-reconciler run independently qualified direct
NOAUTHNOPRIV → AUTHPRIV promotion followed by authentication rotation. Its three
original tasks recovered without replay; exact same-UUID cleanup restored the
baseline. Across both native runs seven original tasks recovered and both
generated definitions were deleted. The [direct-privacy log](snmpv3-metadata-promotion-direct-native.log)
and the [final temporary-API log](snmpv3-metadata-promotion-api.log) are retained.
The final boundary tests cover missing or changed origin evidence at the receipt
publication boundary without modifying live definitions.

## Final checks

- Shared regression: 19,353 passing test events across 15 packages; no failures.
- Final focused race checks: 104 passing test events, including ordinary credential recovery.
- Final temporary API lifecycle: passed with real CRD persistence and fresh commit-boundary refusal checks.
- Both native runs: passed under the race detector; seven original tasks recovered, both baselines restored.
- Production binary build, strict MkDocs build, whitespace check and same-config example validation: passed.
- All 93 unique pinned resource rows remain listed; full-model completion stays false.

The broader regression command compiled before the final extra negative-only
test assertions; those assertions are covered by the final focused race and
temporary-API runs. Production source remained unchanged through every native
and final gate. No private projection or credential file was copied into this
repository, and no previous recovery checkpoint should be replayed.
