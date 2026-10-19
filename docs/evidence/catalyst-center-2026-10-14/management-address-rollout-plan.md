# Management-address declaration rollout boundary

A private declaration planner now consumes the existing controller's retained
verified action receipt and correlated reservation completion, then requires
the common manager-settlement evidence before proposing a CiscoDevice address
change. A Released grant alone, expiry or native inventory convergence is not
enough. The baseline reader now supports Verified receipt evidence as well as
accepted/verifying recovery; this does not grant dispatch or settlement.

The planner binds controller type/incarnation/namespace, old/new address resource
key, native UUID/physical serial, original device generation/UID and Node UID.
It reuses exact managed address agreement and authenticated Node physical
inventory checks. Active maintenance is refused. The returned deep copy changes
only `spec.address`; original inputs, identities and all other fields remain
unchanged. No patch, worker rollout, scheduling-guard removal or new lease occurs.

Fixtures cover a settled proposal and refusal of held grants, absent completion,
missing manager settlement, active maintenance, replaced device/Node, changed
generation, wrong baseline/requested address and paused controller. The combined
race run passed: Catalyst Center 3.941s and shared configuration 3.968s; see
[retained output](management-address-rollout-boundary.log).

This is planning for a staged migration, not an enabled manager action. The
public orchestration must distinguish verified native endpoint work from overall
config/worker convergence, preserve scheduling protection across stages, obtain
fresh source/owner/endpoint authorization, apply the exact declaration change
through the manager and verify the resulting worker revision/incarnation and
physical identity before declaring the complete migration ready. The helper
does not publish a native checkpoint or final receipt by itself.

Installed actors, patch authorization, actual worker rollout and accepted live
migration remain pending. No lab device address changed in this increment.
