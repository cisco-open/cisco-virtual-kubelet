# Retained planned-AP private-state boundaries

## Implementation and scope

Removing a planned AP from a Retain-mode source must not discard its observed
identity or its opted-in private native baseline. Previously the AP planner
queried current source entries only. A later image write, or an unrelated site
write, could therefore proceed without detecting drift in a retained AP removed
from that source.

The private site/design planner now resolves every retained AP through its
recorded floor UUID before querying any AP collection. Native name and hierarchy
must reproduce the original logical resource digest; matching attributes under
a replacement identity do not permit adoption. Missing floors, missing/replaced/
renamed APs, foreign owners, legacy unkeyed anchors and changed private state
refuse before mutation. The existing private baseline reader selects the bounded
latest-applied result when present, otherwise the original observation.

AP collection reads are shared per floor, and complete native observations are
ordered by logical identity for deterministic comparisons. Unknown nested values,
exact numbers, null/absence and array order retain the existing baseline semantics.
Complete snapshots stay in memory; no native payload is added to status or logs.

Before dispatch, the planner repeats the complete retained observation around
the existing source/owner/journal check, including retained AP receipt equality.
The original resource-specific sender still performs its own preflight. Both
task-returning writes and synchronous image uploads use the existing one-use
no-dispatch proof; a native error after invoking the sender cannot obtain that
proof. No transport, shared journal protocol, ND or direct-upgrade change is made.

This is a prerequisite for a floor-to-AP geometry handoff, not that handoff.
Protected floor Updates remain refused when a retained keyed AP references the
floor. This phase adds no AP creation exclusivity, adoption, radio replacement,
public destruction or installed-worker qualification. Full pinned model parity
remains incomplete.

## Qualification

- [27 real private Kubernetes API cases](planned-ap-retained-api.log) pass in
  64.629 seconds. Six new cases remove the AP from source, then attempt a
  synchronous image revision with unchanged, drifted, missing, replaced,
  renamed or late-changed retained AP state. Refused cases produce no image/AP
  mutation; the late-change case proves and retires the undispatched journal.
  Restoring the original simulated AP state permits exactly one image write,
  without changing its original observation. The other 21 cases repeat existing
  keyed-state and type-update coverage. Kubernetes 1.35.0 and repository CRDs
  are real; Catalyst Center HTTP is simulated.
- [Native qualification](planned-ap-retained-native.log) passes in 183.58
  seconds, including cleanup. A new private hierarchy receives an AP, then a
  synchronous image revision while that AP is omitted from the source. Complete
  native AP state remains identical and no AP mutation occurs during the image
  update. The source then reintroduces the same AP for type-only forward/reverse
  `APIW6300DC`/`APIW6300AC` updates. AP/
  radio UUIDs, MAC, coordinates and all unselected native fields are preserved.
  Seven acknowledged tasks recover through Unknown, with five design writes,
  one immutable AP anchor, one bounded latest row and seven total status rows.
  Protected geometry still refuses before mutation. Exact generated AP and
  hierarchy cleanup are verified. No new fixture remains from this run.
- Native HTTP/tasks are real; Kubernetes persistence, projected keys and worker
  restarts are simulated. The test qualifies virtual map design, not physical
  AP/WLC provisioning, device upgrades or installed CVK workers. Negative native
  AP drift is tested through HTTP/API fixtures rather than by altering unrelated
  lab objects.
- [Shared controller, ND, lifecycle and direct-upgrade regressions](planned-ap-retained-foundations.log)
  pass, partly cached. The first invocation used a nonexistent `nd` package;
  the final run uses the actual `nexusdashboard` package and passes.
- [SWIM preparation/recovery tests](planned-ap-retained-swim.log) pass in 2.380
  seconds, including 22 top-level tests and 21 subcases.
- [Pinned resource scope audit](planned-ap-retained-ledger.log) revalidates all
  14 immutable module source files and the same 93 distinct resource rows.
  This is scope accounting, not evidence of complete execution parity. The
  immutable-reference details remain in the [preceding audit](planned-ap-type-upstream-audit.json).
- [Current source fingerprints](planned-ap-retained-sources.json) identify the
  runtime and test files for this phase. Historical phase fingerprints are not
  rewritten.

[Broad configuration/template/site/AP/journal regressions](planned-ap-retained-broad.log)
pass in 235.731 seconds with the race detector. The new retained tests include
14 native-boundary cases, six one-use dispatch cases, a deterministic two-AP
collection/batching case and six complete public-manifest cases. They exercise
latest-applied baselines, missing/replaced/renamed identities, opaque exact-number
drift, source removal, late native/journal changes, synchronous writes and uncertain
sender responses. The full regression log retains all earlier selected tests.

[Strict documentation validation](planned-ap-retained-docs.log) accompanies this
phase. Reproduce native qualification using the keyed/type environment described
in [the preceding phase](planned-ap-type-updates.md), selecting
`TestLiveCatalystCenterPrivateSiteDesign`. The keyed variant now omits the AP
from the source during its second image upload, then restores it for subsequent
updates. Credentials remain in the private runner environment.
