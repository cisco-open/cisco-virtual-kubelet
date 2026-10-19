# Pinned scalar resource selection and retained lifecycle

NetAsCode Catalyst Center 0.5.0 selects NTP, DHCP, DNS, timezone and banner
resources using non-null predicates. DNS selection depends on `domain_name`, not
on the presence of `dns_servers`. CVK now follows these predicates in the existing
resolved network-profile parser and shared configuration executor.

## Behavior

- Null and omitted `ntp_servers`, `dhcp_servers`, `timezone` and `banner` select no
  corresponding resource. Null or omitted `domain_name` selects no DNS resource,
  even when a resolver list is present. Non-null selected fields retain strict
  type/address/domain/timezone/banner validation and unknown keys are rejected.
- A named profile with no selected fields is valid resolved intent. References
  and profile names still pass structural validation. It selects zero resources,
  requires no native site or setting read, and sends no clearing or deletion.
  Explicit empty lists and an empty banner are distinct selected values; DNS
  resolver-reset encoding follows the separately qualified
  [DNS contract](network-settings-dns-optional.md).
- Apply and Report count selected settings, rather than treating one profile/site
  binding as one resource. All six selected setting kinds report a count of six;
  inactive profiles report zero. Other IPAM/credential counts remain additive.
- Retain deselection preserves native values and existing task/source/site-bound
  application rows. Re-selection must still match their original target UUID and
  owner and uses a new source revision/task when an actual update is necessary.
- Zero selection still validates existing receipt custody. It cannot bypass a
  malformed retained application or a corrupt active Verified commit seal.
  Accepted/Unknown work remains bound to the original source; changing that source
  to null cannot settle it, replay it, erase its journal or mint new evidence.

The change reuses the existing transport, common status CAS/owner checks,
no-dispatch guards, task-tree recovery, latest application records, and existing
section composition. No common API/CRD, ND, SWIM or direct-upgrade changes are
introduced. The scalar predicates do not add nullable telemetry/AAA fields or a
schema/default expansion engine.

Immutable upstream reference and current source fingerprints: [source manifest](network-settings-selection-sources.json).

## Evidence

- [Race regression](network-selection-regression.log): 45 retained lifecycle cases
  (five scalar kinds across omission, null, unselected DNS servers, absent native
  site, re-selection, Accepted and Unknown source changes, corrupt active Verified
  seal and corrupt completed history), zero native dependency/report counts,
  malformed selected fields, and existing network, telemetry, DNS, receipt,
  no-dispatch, IPAM, credential, reporting and wired dependency contracts.
  PASS: combined suite completed in 70.347 seconds.
- [Private Kubernetes API](network-selection-api.log): the same 45 retained cases
  store actual controller/config generations, ownership, journals and application
  rows using current CRDs; native responses and actor restarts are fixtures.
  PASS: all 45 cases, 23.971 seconds.
- [Native public cycle](network-selection-native.log): generated empty hierarchy;
  six real settings tasks, original Unknown-task recovery and six latest rows;
  an intervening all-null revision with eight public reconciliations and zero
  native requests, followed by independent actual GETs proving all six values
  unchanged; then six new tasks on re-selection, latest receipt replacement and
  repeated no-op. Kubernetes actors/storage and injected read failures are
  simulated. PASS: 12 original tasks, 12 no-dispatch refusals/retirements, one
  all-null revision and six latest applications (165.686 seconds). Native fixture
  `Global/CVK-Q-20261017-035645-000000000` cleanup and final absence verified.
  Setup contains three site creates and one floor update; cleanup site deletes
  are separate from the twelve setting mutations.
- [Foundation regression](network-selection-foundations.log): ND, controller
  ownership, device operations, diagnostics, software lifecycle/upgrades and IOS
  XE GNOI/runtime packages pass (unchanged packages mostly cached).
- [Strict documentation build](network-selection-docs.log).

The broader regression initially found obsolete null-invalid expectations and an
older global-credential composition fixture returning an object for `/tree`.
Those fixtures now assert null means unselected and return the actual tree-array
shape with the original task ID; they do not relax production completion checks.

Prune/Delete semantics, native baseline capture, ownership release/handoff,
installed workers, physical convergence, broader defaults and full pinned-model
parity remain incomplete. This is a Retain lifecycle, not automatic destruction
of a deselected Terraform-equivalent resource.
