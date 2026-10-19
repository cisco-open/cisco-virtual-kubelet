# Compatible planned-AP type updates

## Contract and implementation

The [pinned provider schema/model](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/resource_catalystcenter_planned_access_point_position.go)
does not require replacement for AP type, and its normal update body carries
`type`. Radio and floor replacement are separate contracts. The documented
[edit DTO](https://developer.cisco.com/docs/catalyst-center/3-1-6/edit-planned-access-points-positions/)
also includes type; actual lab behavior is established by the native cycle below,
not assumed from that API documentation's release.

CVK removes its blanket type-mismatch refusal from the existing bounded Update
payload builder. Native AP/radio identities, MAC, coordinates and omitted settings
are preserved. Type edits do not enable radio mutation or a replacement path.
The existing shared journal, task/readback observer, keyed expected-result receipts
and transport remain responsible for execution and recovery.

Source-selected radio fields alone are insufficient for compatibility: a source
can omit settings or select fewer radios than native state contains. When the
model changes, preflight validates every retained native radio's bands and antenna
against the destination model using the existing authenticated catalog reader.
This check repeats through fresh observations before one-use submission. No
product-ID guessing, model alias rewriting or radio-ID reconstruction is added.

The in-memory submission view now freezes the complete desired AP payload as
well as complete native state. A changed in-memory type cannot cause preflight
to validate one model while dispatching an earlier captured body. Existing source/
owner/journal checks continue to protect externally changed input.

Incompatible retained radios refuse before mutation. Missing receipts remain
uncertain; ignored type changes cannot pass desired-state verification or publish
a keyed latest baseline and do not trigger automatic replay. Unmapped native
update fields and radio changes retain their existing refusal behavior.

## Qualification

- [340 focused checks](planned-ap-type-focused.log), comprising 328 subcases and
  twelve standalone checks, pass in 25.234 seconds. Twelve type cases exercise
  explicit/omitted radios, incompatible antennas/bands, an unselected native
  radio, late catalog/native changes, mutated intended type, radio replacement
  refusal, missing/ignored receipts and no-op behavior. Nine public manifest
  cases join all earlier AP/site/template-native regressions.
- [Nine real private Kubernetes API cases](planned-ap-type-api.log) pass in
  21.408 seconds with Kubernetes 1.35.0 and repository CRDs. They cover plain
  forward update, original-task recovery, omitted radio settings, ignored native
  type changes, incompatible retained antennas, keyed forward/reverse updates,
  keyed task recovery, image composition and keyed ignored-result fencing.
  Product HTTP is simulated. The initial keyed ignored-result test selected the
  wrong fixture branch; the corrected plain/keyed simulations pass without any
  production comparison being weakened.
- The [public catalog selection](planned-ap-type-models.json) identifies
  `APIW6300DC` and `APIW6300AC` sharing the selected 5 GHz
  `AIR-ANT2547V-N-5GHz` antenna. Model/pattern names are public catalog metadata;
  no private inventory or credential values are retained.
- [Final native cycle](planned-ap-type-native.log) passes in 169.52 seconds
  including exact generated cleanup. It creates a new AP on an isolated floor,
  performs image revision, changes only AP type from APIW6300DC to APIW6300AC,
  then reverses that change. Complete native response comparisons preserve AP
  and radio UUIDs, all coordinates, MAC and every other returned field. One
  immutable keyed observation and one bounded latest row remain; seven total
  status rows do not grow across updates. Seven acknowledged tasks recover
  through Unknown without resubmission. Five exact design mutations and three
  subsequent no-ops are counted. The protected geometry edit remains refused
  before native mutation. The exact AP and generated hierarchy are removed with
  readback verification.
- Native HTTP/tasks are real; Kubernetes persistence, restarts, projected keys
  and acknowledged-task uncertainty are simulated. This is virtual map-design
  qualification, not physical AP/WLC provisioning, an upgrade or installed-worker
  qualification. The final native run includes the final frozen-intent guard.
- [Configuration/template/site/design/journal/SWIM regressions](planned-ap-type-broad.log)
  pass in 240.277 seconds. [ND/shared controller/direct-upgrade foundations](planned-ap-type-foundations.log)
  pass, partly cached. The [three operator examples](planned-ap-type-examples.log)
  and [descriptive catalog checks](planned-ap-type-catalog.log) pass separately
  after their additions.

Use `examples/configs/catalyst-center/planned-ap-type-update.yaml` as an alternative
sites-owning configuration. Change only `ap_type` between the qualified pair;
keep the radio/antenna fields unchanged. Existing unowned objects are not silently
adopted by the strict keyed mode. To reproduce native qualification, add:

```text
CATC_AP_TYPE_TRANSITION=isolated-compatible-models
```

to the keyed-state environment in [the preceding qualification](planned-ap-keyed-state.md),
and select `TestLiveCatalystCenterPrivateSiteDesign` with the existing private
credential runner. Do not print endpoint credentials or native private values.

[Source hashes](planned-ap-type-sources.json), the
[93-resource scope audit](planned-ap-type-ledger.log),
[revalidated immutable references](planned-ap-type-upstream-audit.json) and
[strict documentation validation](planned-ap-type-docs.log) accompany this phase.

## Remaining work

This qualifies compatible type edits for the tested pair, not every controller,
model, radio count or automatic native side effect. Radio and floor replacement,
keyed geometry handoff, exclusive creator/adoption policy, consumer protection,
public delete/prune, mixed-version rollout and installed actors remain open.
Full pinned 93-resource parity is incomplete.
