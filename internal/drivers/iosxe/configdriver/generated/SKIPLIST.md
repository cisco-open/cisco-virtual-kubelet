# Per-Family Schema Generation Skip List

Families whose per-family schema package generation was skipped, with the reason for each skip.

A family appears here when `make ygot-validate-gen` cannot produce a valid Go
schema package for it. The generator now uses goyang-based schema extraction
(see `docs/yang-version-support.md`) rather than ygot code generation. The
most common skip reasons are:

- **path-not-found**: the family's `yang_paths` reference a YANG path that
  does not exist in the YANG tree for that release (e.g. a path that was added
  in a later release, or that requires a prefix not in the closure).
- **schema-too-large**: the extracted schema blob exceeds 512 KB (typically
  because the yang_path targets a very large subtree, such as the entire
  `/native` container for the `system` family).
- **no-modules**: the family's `yang_paths` do not reference any module
  prefixes present in the YANG directory, so no module closure can be built.

The fixture harness silently skips schema validation for families listed here
— the standard op-comparison and structural validation still run normally.

**Lenient vs strict mode.** In the default lenient mode the validator skips
unknown fields and type mismatches. Set `CVK_SCHEMA_VALIDATION=strict` to
surface these as hard errors (useful for investigating root causes of skipped
families). See `docs/yang-version-support.md` for details.

## Active Skips

| Release | Family | Reason | Detail | Date Added |
|---------|--------|--------|--------|------------|
| all | system | schema-too-large | yang_path `/native` targets the entire native container (~1.5 MB gzip blob). Blob limit is 512 KB. The system family covers whole-device config; targeted per-family validation requires splitting it into narrower paths. | 2026-06-16 |
| 1715 | access_list_extended | path-not-found | `/native/ip/access-list/extended` not present in 1715 YANG tree. Present in 1718+. | 2026-06-16 |
| 1715 | access_list_standard | path-not-found | `/native/ip/access-list/standard` not present in 1715 YANG tree. Present in 1718+. | 2026-06-16 |
| 1715 | bgp | path-not-found | `/native/router/router-bgp` not present in 1715 YANG tree via the Cisco-IOS-XE-bgp module. | 2026-06-16 |
| 1715 | crypto_ikev2_profile | path-not-found | `/native/crypto/ikev2/profile` not present in 1715. | 2026-06-16 |
| 1715 | crypto_ipsec_profile | path-not-found | `/native/crypto/ipsec/profile` not present in 1715. | 2026-06-16 |
| 1715 | crypto_ipsec_transform_set | path-not-found | `/native/crypto/ipsec/transform-set` not present in 1715. | 2026-06-16 |
| 1715 | crypto_map | path-not-found | `/native/crypto/map` not present in 1715. | 2026-06-16 |
| 1715 | crypto_pki_trustpoint | path-not-found | `/native/crypto/pki/trustpoint` not present in 1715. | 2026-06-16 |
| 1715 | ip_as_path_access_list | path-not-found | `/native/ip/as-path/access-list` not present in 1715. | 2026-06-16 |
| 1715 | ip_community_list | path-not-found | `/native/ip/community-list` not present in 1715. | 2026-06-16 |
| 1715 | ip_http | path-not-found | `/native/ip/http` not present in 1715. | 2026-06-16 |
| 1715 | ip_nat_inside_source | path-not-found | `/native/ip/nat/inside/source` not present in 1715. | 2026-06-16 |
| 1715 | ip_nat_pool | path-not-found | `/native/ip/nat/pool` not present in 1715. | 2026-06-16 |
| 1715 | isis | path-not-found | `/native/router/router-isis` not present in 1715. | 2026-06-16 |
| 1715 | lldp | path-not-found | `/native/lldp` not present in 1715. | 2026-06-16 |
| 1715 | radius_server | path-not-found | `/native/radius/server` not present in 1715. | 2026-06-16 |
| 1715 | tacacs_server | path-not-found | `/native/tacacs/server` not present in 1715. | 2026-06-16 |
| all (1716–2601) | same as 1715 set | path-not-found | Same path-not-found families are skipped across all releases; paths differ by release but the families in the table above are consistently absent. | 2026-06-16 |

## Resolved Skips

| Release | Family | Resolution | Date Resolved |
|---------|--------|------------|---------------|
| 1715–2601 | eigrp | The old ygot-based generator skipped eigrp due to `augment: already set` (Cisco-IOS-XE-eigrp and Cisco-IOS-XE-interfaces both augment interface `ip/summary-address`). The goyang-based extractor navigates the resolved native entry tree directly and does not invoke ygot, so the augment collision no longer applies. eigrp is now generated for all releases. | 2026-06-16 |
| 1718 | bgp | Resolved in the goyang generation path (path present in 1718). Was previously skipped by the ygot generator due to snmp augment conflicts requiring a complex stub. | 2026-06-16 |
| 1718 | class_map | Resolved — goyang extraction does not invoke ygot and is not affected by the atm/ethernet/l2vpn augment conflicts that blocked the ygot path. | 2026-06-16 |
| 1718 | policy_map | Same as class_map. | 2026-06-16 |

