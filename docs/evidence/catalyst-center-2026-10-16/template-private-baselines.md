# Private historical template creation baselines

Project and regular/composite draft creator receipts can optionally include a
keyed MAC of the complete native creation detail. This reuses the existing
PrivateComparisonKey authorization, manager projection and source checks; no
Secret API, store, transport, CRD or provider interface is added.

The exact supported selection is one templates section with one
PrivateComparisonKey reference at /projects. The existing controller-owned
intentSecretSources allow-list must authorize its source alias. A 64-hex-character
random key is projected through the existing manager path. The compiler validates
the model and template assets before resolving that projection.

After the original task tree, UUID, parent and declared body are verified, two
native snapshots must produce the same HMAC-SHA256. Projection/source checks
surround reads and precede atomic receipt publication. A separate domain binds the
key fingerprint to the controller/configuration UIDs and endpoint. The MAC binds
public object identity and full canonical JSON, including unknown fields and
returned references. Raw content, keys and unkeyed private-content digests do not
enter status. Existing inventory comparison key identities are unchanged.

The operation declares whether a baseline is required; stripping or changing its
receipt marker cannot complete the operation. Missing, malformed or changed keys,
changed native bodies and lost receipt fields retain the fence. Old creator
receipts remain valid under their original contract and cannot acquire a baseline
retroactively. Creation receipts stay immutable through later updates.

This is historical creation evidence only. Later updates, commits or project
children can change native state. Latest-applied state, complete version/reference
protection, pruning, deletion and recreation remain pending; a matching historical
MAC grants no destructive authority. Explicit empty template collections now form
a no-op under Retain. Empty omitted intent is still invalid.

## Qualification

- [Focused race cases](template-private-baseline-tests.log): all three creator
  roles cover canonical JSON, private/unknown/reference edits, key rotation,
  identity and receipt corruption, key loss/restoration, native read races and
  mismatched declared fields. Compiler cases cover scope/policy restrictions and
  unchanged inventory key derivation; explicit empty Retain collections are no-op.
- [Real API persistence](template-private-baseline-api.log): 27 cases pass against
  private Kubernetes v1.35.0 with real CRDs/status persistence and controlled native
  HTTPS responses. This is not installed manager/worker execution.
- [Native public cycle](template-private-baseline-native.log): all three creation
  MACs match independent fresh native reads immediately after their original
  receipts, before subsequent mutations. Both source revisions reach InSync;
  nine acknowledged tasks recover without replay, with nine preflight refusals
  safely retired, including three prepared composites. Original creator receipts
  remain unchanged through updates/recommits. Exact generated project/template
  cleanup passes. The native API/tasks and elapsed settling are real; Kubernetes,
  restarts and injected refusal/recovery events are simulated. Test: 398.13 seconds.
- [Configuration/template/wireless race regressions](template-private-baseline-regression.log)
  pass in 217.444 seconds.
- [Foundation regressions](template-private-baseline-foundations.log) pass for the
  shared adapter/configuration, Nexus Dashboard and direct/SWIM lifecycle packages.

Optional manifest: `examples/configs/catalyst-center/template-private-baseline-execution.yaml`.

[Manifest compilation](template-private-baseline-examples.log) and the
[strict documentation build](template-private-baseline-docs.log) pass. No template
was deployed to a physical switch by this qualification. Installed manager/worker
execution and combined installed end-to-end validation remain unqualified.

The subsequent [latest-applied state implementation](template-applied-state.md) adds
separate update/commit snapshots. The creation receipt remains immutable.
