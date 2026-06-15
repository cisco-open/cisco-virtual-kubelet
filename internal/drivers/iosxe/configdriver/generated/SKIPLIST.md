# Per-Family ygot Generation Skip List

Families whose per-family ygot schema package generation was skipped, with the reason for each skip.

A family appears here when `make ygot-validate-gen` cannot produce a valid Go schema package for it. The most common reasons are:

- **augment-collision**: two modules in the closure augment the same YANG path, causing `augment: already set` in ygot.
- **parse-error**: the module set has a YANG syntax error or unresolvable import.
- **no-modules**: the family's `yang_paths` do not reference any modules present in the YANG directory.
- **grouping-chain-conflict**: a module in the closure needs a grouping from a module that must be stubbed to resolve another conflict.

The fixture harness silently skips schema validation for families listed here — the standard op-comparison and structural validation still run normally.

**Gate**: Phase 2 completion requires fewer than 10 entries in this file.

## Active Skips

| Release | Family | Reason | Detail | Date Added |
|---------|--------|--------|--------|------------|
| 1715 | eigrp | augment-collision | Same root cause as 1718 — present across all vendored releases. See 1718 row below. | 2026-06-12 |
| 1716 | eigrp | augment-collision | Same root cause as 1718 — present across all vendored releases. See 1718 row below. | 2026-06-12 |
| 1718 | eigrp | augment-collision | `Cisco-IOS-XE-eigrp` augments 15+ interface `ip/summary-address` paths with `config-eigrp-interface-summary-address-ipv4-grouping` (defines an `eigrp` list), but `Cisco-IOS-XE-interfaces` (native submodule, always in closure) already defines an obsolete `eigrp` list at the same `summary-address` node. ygot sees the node twice. Resolution requires either: (a) a ~4000-line eigrp stub with interface augments stripped (impractical), or (b) ygot support for per-path collision suppression (upstream feature). The eigrp writer only targets `/native/router/eigrp` so the interface schema is irrelevant for validation. | 2026-06-12 |
| 1791 | eigrp | augment-collision | Same root cause as 1718 — present across all vendored releases. See 1718 row above. | 2026-06-12 |
| 2601 | eigrp | augment-collision | Same root cause as 1718 — present across all vendored releases. See 1718 row above. YANG sourced from YangModels/yang `2611` directory. | 2026-06-15 |

## Resolved Skips

| Release | Family | Resolution | Date Resolved |
|---------|--------|------------|---------------|
| 1718 | bgp | Updated `buildConflictStubs` snmp stub to retain `router-snmp-grouping`, `config-priv-grouping`, and `config-access-grouping` (required by isis in bgp's closure), while adding a skeleton `enable/enable-choice/traps` augment anchor so bgp's augment resolves. The colliding bgp nodes from the real snmp module are excluded from the stub. | 2026-06-12 |
| 1718 | class_map | Added atm+interfaces conflict stub to `buildConflictStubs`: strips the colliding `ip` and `load-interval` nodes from `config-interface-atm-grouping` while preserving the `pvc` list (needed by policy augments). Added l2vpn stub for the atm-in-closure case that includes only `config-interface-efp-xconnect-grouping` and omits all `service/instance` augments. Also added `Cisco-IOS-XE-ethernet-cfm-efp` skeleton grouping for the `service/instance` path. | 2026-06-12 |
| 1718 | policy_map | Same root cause and resolution as `class_map`. | 2026-06-12 |

