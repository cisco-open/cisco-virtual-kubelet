# Tag ownership handoff across model sections

The existing tag ownership handoff now permits a fully retired owner to be
replaced by an inventory-only, templates-only or combined tag manifest. Tags
share one native namespace, so this exchanges the whole retired claim. It does
not divide native tag custody between independent owners.

The successor must still use the supported tag-only Apply/Delete shape and
manager enrollment. The worker checks the durable retirement witness, complete
sealed history, absence of the old CR UID, repeated native absence/catalog
stability, and the incoming source/controller snapshot. One existing controller
status CAS archives the old owner's original scope and replaces only its section
claims with the new scope. No native request is made during that exchange.
The next reconciliation prepares the ordinary shared journal operation, binding
its successor Create to the previous archive root and requiring a fresh UUID.

The implementation uses the existing controller status, operation receipts,
source snapshot, transport, cleanup worker and manager completion callbacks.
There is no additional CRD, executor or device upgrade route.

## Qualification

Controlled and real-API lifecycle tests pass all nine old/new section pairs
(18 pair cases across both environments), including all six scope changes,
through Create, Update, CR deletion and a successor's own retirement. Archive,
claim integrity and CAS recovery tests also pass: 114 passing test/package events.
The five shared foundation/ND/upgrade/maintenance/controller packages and main
binary build pass. Broader adapter compatibility also passes (2390
passing test/package events, 450.201 seconds with race detection),
covering selected manifest, discovery, tag, SWIM and reload recovery paths.
See [results and source digests](tag-section-handoff.json).

Native qualification uses generated, unassigned static tags only, isolated
Kubernetes API persistence and a transport that refuses unrelated mutations.
Three three-owner sequences cover all six directional scope changes:
inventory → templates → inventory; inventory → combined → templates;
templates → combined → inventory. ConfigMap recreation and original-task
recovery pass along the same path. Each sequence used three distinct native
UUIDs, three POST/PUT/DELETE requests, three ConfigMap UIDs, six model revisions,
three stale-source refusals and nine original-task recoveries. All nine CR owners
completed cleanup; their original operations and historical scopes remain in the
sealed status. Each run restored its complete native tag catalog baseline.
There were no device operations or installed-cluster mutations.

The native driver uses the existing `TestLiveTagPrunePublicCycle` with
`CATC_TAG_PUBLIC_PRUNE=isolated-owned-tag-cleanup`,
`CATC_TAG_CONFIG_DELETE=manifest-delete`,
`CATC_TAG_RETIREMENT=durable-proof`, `CATC_TAG_HANDOFF=two-successors` and
`CATC_TAG_PROJECTED_SOURCE=mutable-recreated-configmap`. For the first sequence,
set `CATC_TAG_PRUNE_SCOPE=inventory` and
`CATC_TAG_HANDOFF_SCOPES=templates,inventory`; the other sequence selections are
recorded in the qualification JSON. It requires the `live,envtest` build tags,
existing lab authentication environment and a separate writable evidence folder.
Do not rerun a failed native test without examining its retained exact tag UUID
and operation journal first.

## Operator use

Finish and delete the previous enrolled `deletionPolicy: Delete` config first.
Wait until the CR is absent; removing a finalizer manually does not supply the
required retirement proof. Create the successor as a new CR UID, with either
`managedSections: [inventory]`, `[templates]` or `[inventory, templates]` and a
matching tag-only model. Keep the same controller reference and the supported
Apply/Delete contract. `tags-owner-successor-apply.yaml` shows a combined source;
`tags-projected-owner-execution.yaml` shows ConfigMap projection.

For a templates-only successor, for example, use `managedSections: [templates]`
and place the declared `tags` under `catalyst_center.templates`. Do not leave an
inventory section in that source. The manager enrolls the correct lifecycle
finalizer before execution. An unchanged name is allowed when the old CR UID has
actually disappeared; Kubernetes assigns the successor a different UID.

## Remaining boundaries

This does not authorize changing the sections of an active owner in place or
splitting tag ownership. Unclosed legacy owners, missing/corrupt cleanup evidence,
old CRs still present, native resurrection, foreign overlapping claims and lost
exact input for unfinished operations remain fenced. Installed actors, dynamic
owned tags, additional source-prune/recreated-owner combinations and scale limits
remain separate qualification work. Full pinned NetAsCode parity remains open.
