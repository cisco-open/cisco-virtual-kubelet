# AP configuration cohort sequence

## Pinned grouping

NetAsCode 0.5.0 `cc_access_points.tf` groups APs by named configuration and adds a
separate hostname-only group. The new private grouping/planning layer preserves
those keys, including two names with identical settings. Rename-only members
inherit no named configuration or defaults. It consumes parsed configuration-stage
selection; it does not claim to process an entire mixed inventory manifest or
silently discard assignment/provisioning actions.

The common AP freeze helper now validates and orders serial selectors through
the existing canonical physical-identity component. Case-only serial spelling
cannot change member order. Duplicate names/serials and inconsistent settings
within one configuration name are refused before native planning.

## Sequence authority and scaling

The complete cohort selection is preflighted before returning any write. A
sequence digest binds every name, native member set, request and prerequisite
snapshot. Native UUID/serial/Ethernet-MAC aliases across cohorts are refused.
Within a full sequence observation, inventory/site/RF catalogs are collected once
and assignment reads remain scoped by floor. Each revalidation collects fresh
catalogs; there is no cache across authority or attempts.

Each selected cohort still uses the existing one-use native batch planner and
shared authenticated transport. The complete sequence is reobserved before and
after its explicit caller-supplied source/journal/provisioning/physical guard.
A late change to a different cohort cannot authorize an earlier request.

One atomic attempt flag is shared by all plans in that sequence. Success, denial,
stale evidence or uncertainty consumes it. Concurrent attempts on distinct
cohorts admit at most one. Remaining work must be selected by durable journal/
receipt/settlement recovery and a new plan, not by invoking another old closure.
This native layer neither supplies that recovery nor advances any Kubernetes
operation, reservation, actor or lease. A newly constructed plan alone does not
resolve a previous uncertain request.

## Tests

- `TestAccessPointConfigurationCohortGrouping`: named and rename-only cohorts,
  source reordering, same settings under distinct names, canonical serial order,
  duplicate names/serials, missing serial, inconsistent named settings, invalid
  stage state, reserved name and deep input independence.
- `TestAccessPointWholeSequenceDispatch`: four APs across three cohorts, selected
  Alpha or Beta payload/identity, invalid later dependencies, cross-group MAC
  aliases, planning changes, changes to another cohort before/during authority,
  denied authority, uncertain acknowledgement, copied descriptors, source-object
  mutation, concurrent exclusion and refusal of every stale closure afterward.

The first focused run exposed a test-fixture dereference after reordering placed
a rename-only member first. The fixture now finds the actual Alpha configuration
before changing its input settings; no production guard was weakened.

See `access-point-sequence-affected.log` for AP/extranet/SWIM race regressions.
These are affected-test results, not a new full-suite or installed qualification.

## Native qualification

`TestLiveAccessPointSequenceRefusesWiredInventory` uses the actual lab inventory
and the new Go sequence planner, without calling any submission closure. It
verified that three real wired switch identities are rejected at AP identity
resolution before mutation authority is requested. The test passed; see
`access-point-sequence-native.log`. It logs counts only, not credentials or device
identifiers. No controller configuration or device operation was submitted.

There is still no native AP fixture in the lab, so positive AP sequencing,
configuration, task/readback verification and installed actor behavior remain
unqualified. Source/compiler composition, no-op/receipt selection, provisioning
completion, durable submission/acknowledgement recovery, physical scope/admission,
verified receipt publication/settlement and public activation remain pending.
Overall Catalyst Center parity remains incomplete.
