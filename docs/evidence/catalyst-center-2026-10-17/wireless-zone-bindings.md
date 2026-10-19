# Transitive AP-zone RF/SSID identity collection

CVK now has a read-only native identity collector for policy-tag AP-zone
dependencies. It binds the native parent profile UUID, selected zone names,
RF-name-to-UUID mappings and SSID-name-to-UUID mappings from the Global site.
The profile's embedded zones have names, not invented per-zone UUIDs. The
built-in `default-zone` has an explicit built-in marker and no fabricated RF or
SSID identity.

A nil selection collects the complete native parent zone graph; an explicit
empty selection collects none. Selections are bounded, validated before reads
and resolved through the existing paginated RF/SSID/site readers and authenticated
client. Names are preserved exactly, while SSID sets are sorted into independent
snapshots. Missing, duplicate, malformed, sensor or foreign-scope references
refuse resolution. Only used inventory classes are queried, once per collection.
Returned bindings contain public names/UUIDs, never SSID credentials or copied RF
settings. Profile metadata is not modified by collection.

## Qualification

- [28 passing local cases](wireless-zone-bindings-local.log), under the race
  detector (2.395 s): selected/full/empty/built-in scopes, malformed/missing
  references, intended refusal-stage checks, secret exclusion, unused-reader
  checks and same-name RF/SSID UUID replacement. Reordered SSID sets retain the
  same identity digest.
- [Native definition-only qualification](wireless-zone-bindings-native.log)
  passed in 24.81 s (27.234 s including Go overhead). The existing fixture creates
  a generated disabled SSID and profile with two zones referencing existing RF
  definitions. Two complete-parent snapshots and each selected zone match the
  original native RF/SSID UUIDs. Profile/SSID cleanup and unrelated baseline
  restoration pass. No area, tag, site assignment or physical operation is
  submitted in this mode; the collector itself makes only reads.
- [Source/model fingerprints](wireless-zone-bindings-sources.json) record this
  implementation and the pinned NetAsCode 0.5.0 inputs. Earlier milestone source
  inventories describe their qualification-time versions. The reused live
  fixture now has an opt-in collector probe; its ordinary paths are unchanged.

## Integration status

This report records the collector-only qualification milestone. Subsequent
[v4 integration](wireless-tag-bindings.md) now consumes the collector in private
Apply/prune proofs and is undergoing native qualification. At the collector
milestone, Apply/prune dispatch and receipt encodings were unchanged.
The integration must issue versioned proofs, preserve original legacy
recovery semantics, freeze the relevant UUID graph before writes, and verify
dependency bindings through task completion and deletion tombstone publication.
Existing MACs cannot be retroactively treated as containing these new identities.
Recreation/handoff/retirement, installed actors, physical wireless qualification
and the remaining 93-resource lifecycle/model gaps remain open.
