# Native two-AP floor checkpoint qualification

The existing private floor/AP handoff now has native qualification for a second
AP created after the first checkpoint. This phase extends the live harness; it
does not change production admission, transport, effect publication or consumers.

The production planner creates and edits the second AP against its own baseline,
without inserting it into the historical checkpoint. A later width change omits
that AP from source under Retain. The floor operation still protects both native
members and publishes their complete predicted effects. The primary AP consumes
its checkpoint first. Reintroducing the second AP then corrects it independently
against the same floor operation, preserving the complete native object and both
immutable observed anchors. Three subsequent no-op passes issue no writes.

[Native execution](planned-ap-floor-cohort-native.log) passes in 307.32 seconds,
including exact two-AP and hierarchy cleanup. Thirteen acknowledged original
tasks recover without resubmission across simulated restarts; eleven design
writes retain nine bounded action rows. [Focused race regressions](planned-ap-floor-cohort-focused.log)
pass in 14.789 seconds. [Source fingerprints](planned-ap-floor-cohort-sources.json)
record the qualified production and harness files.

Two diagnostic runs stopped on raw fixture assertions: the expected edit encoded
`9` instead of native `9.0`, and the predicted width-only resize encoded untouched
Y as `7` instead of retaining `7.0`. The production keyed comparison already
normalizes modeled position numbers and successfully verified the operations.
Only raw test expectations changed; no production comparison was relaxed and no
pending checkpoint was rebased. The [edit diagnostic](planned-ap-floor-cohort-diagnostic-edit.log)
and [resize diagnostic](planned-ap-floor-cohort-diagnostic-resize.log) are retained.
[Cleanup](planned-ap-floor-cohort-diagnostic-cleanups.log) independently checked
original completed task trees, exact generated identities, coordinates and
hierarchy before removing those two diagnostic fixtures.

This uses real Catalyst Center native APIs/tasks with simulated Kubernetes
persistence and worker restarts. It does not qualify installed actors, physical
AP/WLC deployment, other models/controller versions, replacement, adoption or
public destructive lifecycle. Full pinned NetAsCode parity remains incomplete.
[Strict documentation build](planned-ap-floor-cohort-docs.log).
