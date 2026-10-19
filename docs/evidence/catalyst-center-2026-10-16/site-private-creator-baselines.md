# Private native creator baselines for singular sites

## Scope and implementation

New singular area/building/floor creates can opt into an approved
`PrivateComparisonKey` reference at section `sites`, operational scope `/sites`.
The existing projected-key resolver, source checks, transport, one-use dispatcher,
configuration journal and atomic receipt persistence are reused. There is no new
credential store, Secret API reader, CRD field or independent lifecycle machine.
The operational reference is outside the unchanged pinned NetAsCode site model.

A separate `site-private-create:v1` producer identity binds kind, public path
hash, inline/ConfigMap source mode and domain-scoped key identity. The new
`site-created:v2` receipt additionally binds the original created UUID and a keyed
native-state MAC. Required producer/key/source fields cannot be pruned into a
legacy unkeyed ownership interpretation. Existing v1 creator receipts remain
separate and are not retroactively given private baselines.

Native reads reuse existing completeness, paging and duplicate-ID checks. Each
raw row is decoded with exact JSON numbers; its typed identity/projection must
agree with the validated ordinary site collection. The retained historical
footprint includes the **complete native target object**, including unknown and
nested fields, plus ancestor identities. Native values remain in memory. Only a
keyed, domain-separated MAC enters status; no raw value, raw key or unkeyed
private-value digest is published. The MAC includes the key identity, binding it
to the controller/config/endpoint scope established by the existing resolver.

The pre-submit footprint additionally includes complete ancestor objects. Late
unknown-field edits, numeric edits and partial raw reads refuse dispatch with
the existing original guard-bound no-dispatch capability. An actual dispatched
error acquires no such capability, and the sender cannot be reused.

Keeping historical ancestor **identities** separate from pre-submit ancestor
settings avoids coupling every child's ownership baseline to a future legitimate
parent-setting update. Parent replacement remains significant. No stored private
baseline is normalized or silently recomputed after drift.

At creator verification, complete task-tree/result-UUID and typed desired-state
checks precede repeated raw observations and keyed publication. Restarted no-ops
must match the stored footprint with the same approved key. Missing/rotated keys,
removed options, changed unknown native state and attempts to backfill imported
or legacy unkeyed objects fail closed. This is the first task-bound verified
creation observation; it is not an appliance-signed historical body or evidence
that no external writer ever acted between acknowledgement and capture.

## Qualification

- [51 focused checks](site-private-creator-focused.log), consisting of 48
  subcases and three standalone tests, pass in 11.125 seconds. They cover
  producer/receipt/schema/source boundaries, legacy aliases, exact numbers above
  2^53, null-versus-absent fields, unknown fields, object/key/controller scope,
  public create/recovery/no-op, post-create drift, key/option loss, no backfill,
  pre-submit refusal and sender reuse. The scope-separation test keeps parent
  settings out of historical child baselines while retaining them in dispatch
  observations. Large exact counters are synthetic fixture fields; they are not
  claimed as observed appliance counters.
- [15 private Kubernetes API cases](site-private-creator-api.log) pass in
  37.708 seconds including API startup/teardown. Real CRDs on Kubernetes 1.35.0
  must retain producer/key/MAC/source fields across public restarted
  reconciliation. Native HTTP is simulated. These are schema/persistence checks,
  not installed worker or manager projection qualification.
- [Final native cycle](site-private-creator-native.log) passes in 45.40 seconds
  including exact generated cleanup. A complete three-site manifest creates an
  isolated area/building/floor hierarchy. All three original acknowledged tasks
  recover through Unknown and publish private creator receipts. Independent task
  result UUIDs bind exact native kind/path/parent. Three further restarted public
  reconciliations reach unchanged-state InSync with no new operation. Full native
  target fields include the appliance's additional site hierarchy identity field.
  Native HTTP/tasks are real; persistence, projection files, worker restarts and
  uncertainty injection are simulated. No device, PnP claim or SWIM action occurs.
- [Configuration/template/site/design/journal/SWIM regression](site-private-creator-broad.log)
  passes in 204.039 seconds. [ND/controller/direct-upgrade foundations](site-private-creator-foundations.log)
  also pass. Some unchanged foundation results are cached.

The execution example is repository source outside the documentation tree. Use
`examples/configs/catalyst-center/site-private-creator-execution.yaml` from the
repository root. It declares three fresh sites, the operational key reference,
Retain policies and explicit singular selection. Never insert the key into the
model or source history.

Reproduce the isolated native qualification with privately loaded credentials:

```text
CATC_SITE_EXECUTION=isolated-hierarchy
CATC_SITE_PRIVATE_BASELINE=isolated-creator-baselines
CATC_SITE_TASK_RECOVERY=isolated-acknowledged-tasks
CATC_LIVE_REQUESTS_PER_SECOND=2
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenterSitePrivateCreatorBaselines$' -count=1 -v
```

The existing CATC endpoint/username/password variables are also required and must
not be printed. [Source hashes](site-private-creator-sources.json),
[93-resource scope audit](site-private-creator-ledger.log) and
[strict docs](site-private-creator-docs.log) accompany the qualification.

## Remaining work

This phase implements private **creator** baselines, not latest-applied Update
receipts. Private updates are explicitly refused before submission so creator
history cannot be silently overwritten. Existing nonprivate Retain updates retain
their original path. The private Update protocol, capacity/lineage replacement,
full task recovery and live create/update cycles remain next work.

Bulk creator lineage, floor-image/planned-AP composition with the private key,
complete dependency/consumer protection, public prune/delete and atomic
destructive settlement remain pending. The live test's bounded exact-ID cleanup
is not public manifest-driven deletion. Installed workers, actual manager-mounted
key projection, mixed-worker/schema rollout and the remaining resource ledger
still require qualification. Full pinned-model parity remains incomplete.


## Subsequent implementation

The [latest-applied Update phase](site-latest-applied.md) now implements the
private Update protocol and qualifies bounded replacement, parent/child evidence
and recovery. It supersedes the Update-gating limitation in this historical
creator-only phase; the original source hashes and execution results above
remain the record of that phase.
