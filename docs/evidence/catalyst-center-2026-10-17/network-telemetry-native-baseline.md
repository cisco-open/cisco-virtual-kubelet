# Retained private telemetry baseline

The optional private-comparison mode retains a keyed proof of the complete known
effective telemetry profile and exact site ancestry alongside the existing
operation and latest application receipt. It reuses the controller-approved
projected key resolver, shared client, journal, status writer and reconciliation
checks. No new CRD field, Secret API reader, transport or private state store is
introduced. Other providers and direct-device/SWIM flows retain their contracts.

## Configuration and execution

The example is `examples/configs/catalyst-center/telemetry-native-baseline.yaml`.
Use a new configuration owner or an independently qualified handoff. The owning
NetworkController must approve the existing comparison source, for example:

```yaml
intentSecretSources:
  - alias: comparison
    name: network-native-comparison
    key: key
```

The existing worker projection must supply an independent 32-byte hex key through
that approved source. Key values do not belong in the model or checked-in sample.
The configuration selects only `network_settings`, uses Retain policies, and
references `PrivateComparisonKey` at `/network_settings`. This mode currently
supports network/telemetry profiles and site references, rather than composing
credential definitions, AAA or IPAM declarations in the same source.

Telemetry writes freeze the full known native profile, overlay the selected
source fields and bind that expected post-state and target-to-Global identities
to the original operation. The v2 resource contains the logical-setting key,
public bookkeeping predecessor, comparison-key identity and expected-state MAC.
The MAC uses a domain distinct from key identity and public receipt sealing.
Neither raw native values nor an unkeyed digest of private native values is
persisted. Public bookkeeping digests are not native-state attestations.

Completion and restarted observation require the original task and full task
tree plus consistent native observations and the original expected-state MAC.
A successful later revision atomically replaces the latest v2 receipt after its
own task and verification. A matching imported no-op creates no baseline. A
missing acknowledgement remains taskless Unknown and is never replayed or
inferred successful from matching settings.

A retained v2 receipt fences key removal/rotation, private-proof downgrade,
external changes to unselected modeled values, ancestry replacement and renaming
the same native target into another logical path. Restoring the original key or
expected native state permits the original acknowledged task to be observed;
it does not authorize another submission. Malformed proofs are refused.

## Qualification and evidence

- [Comparison and projected-key regression](network-telemetry-baseline-comparison.log):
  17 telemetry lifecycle cases, eight direct MAC binding cases, the example
  compiler, and existing site/template private-comparison regressions.
- [Temporary Kubernetes API qualification](network-telemetry-baseline-api.log):
  the same 17 telemetry lifecycle cases, including retained receipt replacement,
  companion drift, key restoration/loss, legacy refusal and same-UUID rename.
- [Actual Catalyst Center qualification](network-telemetry-baseline-native.log):
  three partial telemetry revisions preserve all known unselected groups,
  recover their original acknowledged Unknown tasks, retain/replace v2 proofs,
  verify repeated no-op and remove the isolated hierarchy. The native API is real;
  Kubernetes storage, actors and the approved key projection are fixtures.
- [Legacy network regression](network-telemetry-baseline-legacy.log) and
  [shared foundation regression](network-telemetry-baseline-foundation.log).
- [Strict documentation build](network-telemetry-baseline-docs.log).

The actual API fixture hierarchy was
`Global/CVK-Q-20261017-051033-000000000`; cleanup was verified. No device upgrade,
reboot, PnP claim or installed-cluster repair was part of this qualification.
The final rename guard was qualified locally and against the temporary Kubernetes
API after that live cycle; the live cycle did not exercise a native site rename.
[Source fingerprints](network-telemetry-native-baseline-sources.json) identify the
final implementation separately from the earlier live execution snapshot.

## Limits still open

This mode is explicit opt-in: existing v1 no-key behavior remains supported.
Existing v1 telemetry receipts cannot silently become stronger v2 proof; legacy
migration, key rollover and ownership/hierarchy rebase need dedicated flows.
Scalar network-setting receipts remain v1. Multi-target mutation ordering,
installed workers/key mounts, external collector behavior, destructive cleanup
and physical effects remain separately unqualified. A MAC grants no creator,
adoption, deletion, prune or ownership-handoff authority.

The proof covers known modeled effective values and site identities. Strict
readers refuse unknown native fields instead of dropping them. It does not prove
packet forwarding or collector delivery, or distinguish every possible change
of inheritance origin that leaves effective values identical. Trusted Kubernetes
status remains required: total receipt/status erasure is not distinguished from
fresh state, and arbitrary coordinated control-plane corruption is not solved.

Full NetAsCode schema/default/merge and lifecycle parity across all 93 catalogued
resource types remains incomplete. This is one bounded execution/recovery gap,
not a claim that the Catalyst Center branch is ready to merge.
