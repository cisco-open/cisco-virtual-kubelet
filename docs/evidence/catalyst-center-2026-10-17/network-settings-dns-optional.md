# DNS optional resolver semantics

The pinned NetAsCode 0.5.0 module selects DNS when `domain_name` is non-null;
its pinned Catalyst Center provider 0.6.1 omits `dnsServers` for null/absent input.
CVK now accepts a domain with omitted or null `dns_servers`, retaining a separate
intent from an explicit empty list. This uses the existing settings executor,
transport, inheritance resolver, preflight, task recovery and application receipt
contracts; no foundation, ND, upgrade or SWIM schema changes are introduced.

## Native behavior and execution policy

A direct isolated lab probe found that a domain-only PUT **clears** existing
resolvers. Sending `dnsServers: []` instead returns HTTP 400. Consequently:

- Omitted/null model `dns_servers` leaves resolver drift unmanaged. When a domain
  change requires a PUT, CVK copies the effective resolver list into the payload
  from the same frozen native observation. Both pre-submit observations must still
  match, including these unselected values and their actual ancestor identities.
- Explicit `dns_servers: []` requests resolver clearing. The native request omits
  `dnsServers`, using the observed native clear behavior instead of the rejected
  empty-array encoding. This is a resolver reset, not removal of the DNS group.
- Existing nonempty resolver lists retain their order. Native absent/null resolver
  lists are accepted as empty only with a valid explicit DNS domain; malformed
  addresses/types and missing/null domains remain observation failures.
- A matching domain-only profile neither writes nor invents an application
  receipt. Native settings are not silently adopted.

This preserves omitted resolvers more conservatively than the pinned provider's
raw PUT serialization. If resolvers were inherited, an actual domain override
copies the current effective list into the native group; it does not implement
independent per-field inheritance or follow future parent resolver changes.
There is no new public native-value baseline, per-field ownership ledger or
rollback claim. The operation's existing source digest binds its declared mask.

Source fingerprints and immutable upstream references: [source manifest](network-settings-dns-optional-sources.json).

## Qualification

- [Race regression](dns-optional-regression.log): optional/null/empty model intent,
  inherited resolver preservation, stale unselected resolver preflight refusal,
  malformed native reads, full public recovery/receipts/no-op and existing
  network/telemetry/AAA/no-dispatch contracts. PASS (50.769 seconds), including
  16 new DNS cases and the existing combined contracts.
- [Private Kubernetes API](dns-optional-api.log): five optional DNS public cases
  persist actual controller/config status using current CRDs. Native responses
  and interruptions are controlled fixtures. PASS (10.040 seconds).
- [Native public flow](dns-optional-native-final.log): generated empty hierarchy,
  resolver fixture setup, three source revisions (omitted/preserve, explicit
  empty/clear, null/preserve-empty), original acknowledged task recovery,
  preflight refusal retirement, latest receipt replacement and repeated no-op.
  Kubernetes storage and restarts are fixtures; Catalyst Center tasks/readback
  are real. PASS: three public mutations, three original task recoveries and
  exactly one latest application on `Global/CVK-Q-20261017-034850-000000000`;
  final hierarchy absence verified. Setup contains three site creates, one floor
  update and one resolver seeding task, separate from the three public DNS tasks.
- [Initial probe](dns-optional-probe.log) records the unexpected API behavior and
  rejected empty-array request. [Exact probe cleanup](dns-optional-cleanup.log)
  verifies original creator-task UUIDs, geometry, empty assignments, unchanged DNS
  footprint, full delete task trees and final absence. A subsequent harness-only
  section-selection failure was corrected; its exact cleanup precedes the final
  native public flow in the final log.
- [Foundation regression](dns-optional-foundations.log): ND, controller ownership,
  device operations, diagnostics, software lifecycle/upgrades and IOS XE
  GNOI/runtime tests pass (unchanged packages, cached).
- [Strict documentation build](dns-optional-docs.log).

Existing sites, global settings and device configurations are outside native test
mutation scope. Installed controller actors, permanent private baselines,
per-field inheritance, group deletion/pruning, handoff, broader defaults/scale,
physical rollout and full 93-resource model parity remain incomplete.
