# Per-Family Schema Generation Skip List

Families whose per-family schema package generation was skipped, with the
reason for each skip.

A family appears here when `make ygot-validate-gen` encounters a reviewed
domain limitation. The generator uses goyang-based schema extraction (see
`docs/yang-version-support.md`) and permits only these skip codes:

- **path-not-found**: the family's `yang_paths` do not resolve in the YANG
  tree for that release.
- **schema-too-large**: the extracted schema blob exceeds the 512 KB
  per-family limit.
- **no-modules**: the family's `yang_paths` do not reference any module
  available in the pinned YANG directory.

The machine-readable source of truth is
`../schema/yang-skip-baseline.yaml`. Generation compares the exact
release/family/reason-code set against that file. A newly skipped family, a
resolved skip, or a reason-code change fails CI until the generated artifacts
and baseline change are reviewed together. Parser, schema-builder, filesystem,
registration, and validator-index errors cannot be baseline skips and always
fail generation.

The fixture harness skips YANG schema validation for these families; standard
operation comparison and structural validation still run normally.

**Lenient vs strict validation mode.** In the default lenient mode the
validator skips unknown fields and type mismatches. Set
`CVK_SCHEMA_VALIDATION=strict` to investigate those fixture/schema
discrepancies. This runtime fixture setting is separate from the strict
generation and skip-baseline gate described above.

## Active Skips

Each supported release (`1715`, `1716`, `1718`, `1791`, and `2601`) currently
has the same 19 reviewed skips:

| Reason code | Families | Detail |
|-------------|----------|--------|
| `path-not-found` | `access_list_extended`, `access_list_standard`, `bgp`, `crypto_ikev2_profile`, `crypto_ipsec_profile`, `crypto_ipsec_transform_set`, `crypto_map`, `crypto_pki_trustpoint`, `event_manager`, `ip_as_path_access_list`, `ip_community_list`, `ip_http`, `ip_nat_inside_source`, `ip_nat_pool`, `isis`, `lldp`, `radius_server`, `tacacs_server` | The declared `yang_paths` do not resolve in the extracted native schema for the pinned release bundle. |
| `schema-too-large` | `system` | Its `/native` target produces an approximately 1.5 MB gzip schema blob, above the 512 KB limit. It needs narrower paths before targeted validation can be enabled. |

That leaves 35 of 54 families with usable generated validators in each
release. The baseline records the stable reason code, while each generated
`schema.go` skip stub contains the release-specific path or size diagnostic.

## Resolved Skips

| Release | Family | Resolution | Date Resolved |
|---------|--------|------------|---------------|
| 1715–2601 | eigrp | The old ygot-based generator skipped eigrp due to an augment collision. The goyang extractor can now navigate the resolved native entry tree, so eigrp is generated for every release. | 2026-06-16 |
| 1718 | class_map | Goyang extraction is not affected by the atm/ethernet/l2vpn augment conflicts that blocked the old ygot path. | 2026-06-16 |
| 1718 | policy_map | Same resolution as class_map. | 2026-06-16 |
