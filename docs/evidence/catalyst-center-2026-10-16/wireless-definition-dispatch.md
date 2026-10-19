# Wireless definition submission snapshots

Date: 2026-10-16. Pinned NetAsCode module 0.5.0. Follow-up to the existing
[anchor guard](wireless-anchor-multipeer.md),
[retained-task recovery](wireless-definition-recovery.md) and
[complete Update task trees](wireless-definition-task-trees.md).

## Implementation

Interface, power-profile, 802.11be, pre-auth ACL and RF-profile submissions now
share `guardWirelessDefinitionWrite`. It reuses the existing atomic one-use
wireless dispatch wrapper; no new transport, provider interface, worker or journal
was added. Each reader retains its existing native validation.

Planning captures the current compiler intent and selected typed native state.
Submission first compares current intent, then rereads native state before
calling the original mutation closure. A Create target must remain absent; an
Update requires the original native ID and observed known fields. External
changes to omitted-but-preserved power descriptions, 802.11be MLO/default values
or ACL URLs cannot silently be overwritten. Intent changes also fence dispatch,
including ACL nil-versus-explicit-empty URL ownership and private RF nested maps.

RF protects its whole typed collection because default selection has effects on
other profiles. Other new paths protect the selected object and allow unrelated
valid inventory additions. Comparison uses typed values, not serialization of RF
metadata with private fields. Fresh reader-owned snapshots are not mutated by
payload composition. Intent encoding has explicit projections for RF and ACL
presence semantics. Request payload/default semantics otherwise remain unchanged.

The closure is consumed on refusal, read failure, uncertain acknowledgement,
success or a concurrent attempt. A retained closure cannot issue another HTTP
write. This is a repeated native observation, not an atomic appliance conditional
write or proof about unmapped attributes. Existing durable source/owner checks
remain in the common journal. Deterministic pre-submit refusal after the journal
enters Submitting can still require supervised no-dispatch recovery; automatic
retirement is not added here. Lost/taskless outcome evidence is never guessed.

## Local and API qualification

- [Dispatch race tests](wireless-definition-dispatch-tests.log) pass in 3.117s:
  50 new cases across five executors plus the existing anchor snapshot cases.
  They check stable submission, target replacement/removal, native field edits,
  unrelated additions, caller intent mutation, failed reads, uncertain receipts,
  concurrent use, newly appeared Create targets and consumed closure reuse. Exact
  mutation counts distinguish preflight refusal from a rejected HTTP request.
- [Wireless/task regression](wireless-definition-dispatch-regression.log) passes
  in 97.874s. This includes existing field/default behavior, public combined
  manifests, acknowledged Update recovery, task-tree refusal and SSID history.
- [Private Kubernetes API-server tests](wireless-definition-dispatch-api.log)
  pass in 23.900s: all 48 definition recovery/persistence cases remain passing.
  Native services there are controlled HTTP fixtures; the real lab cluster is
  not started or changed.
- [Foundation checks](wireless-definition-dispatch-foundations.log) pass with
  race checking for adapter registration, complete ND, shared software lifecycle
  and software-upgrade packages. Their production logic is unchanged. This is
  not a full CC-package or installed rollout claim.

## Native stale-plan qualification

`TestLiveWirelessDefinitionRecovery` with both
`CATC_WIRELESS_DEFINITION_RECOVERY=isolated-definitions` and
`CATC_WIRELESS_DEFINITION_STALE=isolated-definitions` passes all six native cases
in 32.32s (package 34.678s). See the [native log](wireless-definition-dispatch-native.log)
and [completion receipts](wireless-definition-dispatch-complete.json).

For each generated unused definition, a private retained Update plan captures the
original state. The public reconciler then performs its legitimate Update, with
acknowledged Unknown recovery and full task-tree/readback verification. Attempting
the old plan returns the stale-observation error before HTTP; reusing it returns
the consumed-transition error. The request trace confirms that both attempts add
zero mutations. Three public no-op passes add none either. Exact captured-ID
cleanup completes its original task and restores the original typed inventory
baseline. Each case has one Create, one Update and one Delete: 18 total attempts,
six stale refusals and six cleaned fixtures. Native qualification uses actual
controller APIs with simulated Kubernetes persistence, not installed workers.

An initial run passed four cases, then stopped after ACL Create because a numeric
fixture name contained `443`: harness text substitution changed the name before
the intended port leaf. No ACL Update was submitted. Exact native ID and full
unchanged ACL state were checked before cleanup task
`01a1438c-6b90-75ac-9876-fec16873c397`; the empty ACL baseline was restored.
The harness now substitutes bounded model values before expanding generated names.
Final ACL and RF fixtures deliberately contain `443` and `100` respectively,
proving that model updates preserve their names. No preliminary fixture remains.

## Remaining scope

The guards do not establish Create-result ownership, adoption, pruning, automatic
no-dispatch retirement, taskless recovery or physical WLC/AP deployment. Those
and installed-worker/scale qualification remain pending in the resource ledger.
Full 93-resource parity remains incomplete; whole-resource flags stay false.


A subsequent scoped implementation now qualifies automatic live-proof retirement
for these six definition paths; see [no-dispatch recovery](wireless-no-dispatch.md).
The older taskless/crash and other-resource limits still apply.
