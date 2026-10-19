# Projected tag lifecycle and handoff

The existing tag executor, source snapshots, operation journal and ownership
handoff now pass native qualification with ConfigMap-backed model data. No
production runtime change was needed; this phase extends the existing guarded
qualifier and reuses the prior qualified runtime.

Each scope used three different ConfigMap UIDs, two model revisions per UID and
three sequential config owners. Create and Update retained the exact ConfigMap
UID/resourceVersion in their original operation and receipts. Model-only updates
converged without a config-generation change. After each revision update, the
previous source snapshot failed the normal dispatch snapshot check.

The source ConfigMap was removed only after its Create/Update work was verified,
then the CR was deleted. Cleanup used its settled receipts and synthetic
finalization source, with no mutable ConfigMap binding on Delete. The next owner
received a newly created same-name ConfigMap with a different UID. The existing
old-CR absence, sealed audit and shared claim/journal checks governed handoff.

## Native results

All three scopes passed against Catalyst Center through an isolated Kubernetes
API. Each completed original owner plus two successors, with exactly three
POST/PUT/DELETE requests, three distinct native tag UUIDs, nine recovered original
tasks, six model revisions and three stale-source refusals. Full native tag
catalog baselines were restored. The strict transport allowed no device mutation.

| Scope | ConfigMap epochs | Model revisions | Stale snapshot refusals | Original tasks recovered |
| --- | --- | --- | --- | --- |
| inventory | 3 | 6 | 3 | 9 |
| combined | 3 | 6 | 3 | 9 |
| templates | 3 | 6 | 3 | 9 |

The same shared controlled/real-API lifecycle fixture also passes projected
handoff in all three scopes. See [qualification data](tag-configmap-lifecycle.json)
and the [final inventory receipt ledger](tag-configmap-native-records/inventory/handoff-1-closed-and-gone.json).
The example `tags-projected-owner-execution.yaml` contains its ConfigMap and
NetworkControllerConfig; it keeps the same config name as the inline policy
revision so users preserve UID ownership when changing the source representation.

## Boundaries

This is native controller plus isolated Kubernetes persistence qualification.
Installed deployment/projection and physical-device execution are not claimed.
Source removal is qualified after completed work; losing the exact input of an
already accepted operation remains fenced and does not trigger replay. Inventory
source loss before any tag receipt cannot infer its resource family. Dynamic or
recreated-owner combinations, section handoff, legacy migration and scale limits
remain separate gaps. The full pinned NetAsCode model is still incomplete.

Production code is unchanged from the prior qualified handoff runtime. The
projected execution changes are in the existing live qualifier and shared
controlled/API test selection. A final example-only test was added after native
execution; both corpus hashes are retained in the qualification JSON. Existing
foundation, ND, direct-upgrade, SWIM and manager evidence is reused for the exact
same production digest rather than claimed as new device execution.
