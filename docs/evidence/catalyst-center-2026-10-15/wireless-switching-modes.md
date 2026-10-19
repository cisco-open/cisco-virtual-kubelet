# Wireless profile switching-mode validation

Cisco's [Create Wireless Profile API](https://developer.cisco.com/docs/catalyst-center/create-wireless-profile/)
disallows simultaneous Fabric and FlexConnect settings within one profile.
The adapter validates the complete merged payload, including retained SSID flags
and an omitted SSID collection. It rejects either a single SSID with both modes
or distinct SSIDs that introduce the incompatible combination.

The public reconciler reports `MixedWirelessSwitchingModes` and Failed without
preparing an operation or submitting configuration writes. A manifest test puts
a valid profile before an invalid profile and restarts reconciliation three
times, asserting zero writes and no operation. Payload tests also verify retained
observations are unchanged and explicit false removes stale local VLAN settings.
Central SSIDs alongside either Fabric or FlexConnect are not rejected by this
specific constraint.

This is adapter-local validation through existing payload and error handling.
No controller protocol, journal schema, ND executor or transport was changed.
Explicit flag changes passing model validation are not proof of native mode
migration or actual WLC/AP behavior. The separate lab cycle exercises ordinary
profile metadata, not a Fabric/FlexConnect migration. Full resource parity remains
open.

Validation results are recorded in the companion `wireless-switching-*.log` files.

Final checks on this revision:

- Targeted switching-mode race tests: pass (2.817s).
- Wireless/profile and SWIM race regressions: pass (10.256s).
- ND and shared controller, configuration reservation and managed protocol: pass (cached).
- Manager/connector/worker RBAC integration: pass (3.779s).
- Native isolated disabled-SSID/profile create, same-ID update, readback/no-op
  and exact cleanup: pass (15.506s overall). Public baselines restored; no site
  assignment, device provisioning or device operation was submitted.
- Strict documentation build: pass (4.44s); whitespace check: pass.

The whole Catalyst Center package suite was not rerun for this isolated validation
change; the scope above is the verification performed on this revision.
