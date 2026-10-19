# CleanAir selected-state dispatch qualification — 2026-10-16

The existing CleanAir executor now snapshots every selected definition before
preparing a write. Just before dispatch it rereads the selected summary identities,
system-template flags and native details, including already-converged siblings.
An absent target must still be absent. UUID replacement, system protection changes,
modified omitted description/interferer/unlocked fields, sibling drift and changed
in-memory desired input refuse the prepared write. Unselected definitions are not
locked by this comparison. The native service still provides no atomic
compare-and-swap between the final read and PUT.

Each prepared closure is consumed once, including stale preflight and uncertain
submission failures. The existing configuration journal continues to govern
subsequent reconciliation and task recovery; this does not add durable resource
ownership, imported-resource adoption, source pruning or a new recovery protocol.
No public API, CRD, shared provider or device upgrade implementation changed.

The pinned provider 0.6.1 schema allows radio_band without a replacement modifier,
and its Update serializes radioBand into PUT. That is insufficient evidence of
native cross-band compatibility. At that stage CVK continued to refuse band changes,
with an accurate qualification message rather than asserting replacement is the
required upstream behavior. Band transitions were a resource-ledger gap at this stage; subsequent native
and manifest qualification is recorded in [band-update evidence](cleanair-band-updates.md).

## Verification

Focused CleanAir field, public manifest restart/update, complete combined preflight
and dispatch-fence tests pass with race detection (3.070s,
`/tmp/cvk-cleanair-dispatch-tests.log`). The dispatch matrix covers preserved fields,
identity, system flag, converged sibling, concurrent appearance, desired mutation,
stable submission and malformed task receipt; all cases check consumed-permit refusal.
The final dispatch matrix also passes sixteen concurrent submission attempts with
exactly one native write (2.039s, `/tmp/cvk-cleanair-concurrent-dispatch.log`).
The final wireless/composition selection passes (12.451s,
`/tmp/cvk-cleanair-final-tests.log`); complete model tests pass separately
(`/tmp/cvk-cleanair-model-final.log`). The earlier broader selection passes (11.885s,
`/tmp/cvk-cleanair-wireless-regression.log`). Model, Nexus Dashboard, migration,
IOS XE software lifecycle and software-upgrade packages pass, with the latter
14.466s (`/tmp/cvk-cleanair-foundations-final.log`). An initial command referenced
nonexistent internal/migration; that setup failure was corrected to the actual
tools/cvk-netascode-migrate package in the successful final run.

Native isolated-definition qualification passes (2.74s; race package 4.870s,
`/tmp/cvk-cleanair-native-stale-dispatch.log`). Generated definition
`CVK_16022348`, UUID `c01658ad-8573-4368-b91a-71aa2d666239`:

- Create: task `01a14285-d182-791c-88b3-a48f72156acf`; prepared-create reuse refused.
- A separate generated-only description update completed through the same
  executor: task `01a14285-d4f1-7ed5-8b46-44d7db867b4b`.
- The previously planned enable update refused the stale native observation;
  a second attempt refused the consumed permit without sending a mutation.
- Fresh enable update: task `01a14285-d775-75da-a655-d46e8d8679ba`;
  UUID/readback and no-op verified, with omitted native description preserved.
- Exact generated UUID cleanup: task `01a14285-d9bf-7981-994f-7c4c8c7fe165`;
  summary baseline restored after verified completion.

An earlier guarded create/update/cleanup also passed before adding the concurrent
native edit (`/tmp/cvk-cleanair-guarded-native.log`). No WLC/AP association,
wireless deployment, physical device operation or installed-cluster recovery
was performed or qualified. Full CleanAir and whole-model parity remain incomplete.
