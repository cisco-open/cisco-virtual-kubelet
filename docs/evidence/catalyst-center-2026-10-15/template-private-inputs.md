# Private template input comparison

This evidence covers implementation and local reconciliation fixtures, not an
installed Kubernetes or native CLI deployment qualification.

## Contract

Approved random 32-byte keys are projected through the existing intent Secret
allow-list as 64 hexadecimal characters. No authentication password is reused.
A single inventory-scoped operational reference enables the comparison:

```yaml
secretRefs:
  - section: inventory
    path: /devices
    source: template-comparison-key
    use: PrivateComparisonKey
```

The alias must also be administrator-approved in the NetworkController's
`intentSecretSources`. Upgrade both CRD copies before using the optional field.
Keep the key stable for the lifetime of comparable receipts. Key rotation and
same-version legacy receipts hold instead of guessing a previous parameter set.
A new explicit committed version or revision-scoped ALWAYS deployment can seed
new comparable evidence through the ordinary admission flow.

Comparison fingerprints are domain-separated HMAC-SHA256 values; raw parameters,
unkeyed parameter hashes and keys are excluded from status and diagnostics.
Canonical target inputs follow the actual pinned provider implementation:
[provider 0.6.1 deployment resource](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/resource_catalystcenter_deploy_template.go).
Global copying/force flags do not affect target-change comparison. Composite
member order does. Shared operation digests bind the optional fingerprint and
retain the existing ordered template-version graph.

## Validation

- Pure comparison tests cover formatting, option changes, parameter changes,
  redaction, regular/composite resources, ordered members and invalid suffixes.
- Receipt policy tests cover latest verified effect selection, A-to-B-to-A
  rollback, unrelated revisions, key rotation, legacy receipts and tied history.
- Shared contract tests reject unadvertised/unknown uses and require projected
  files even for adapters advertising private comparison.
- Projection tests reject value injection of comparison keys and detect rotation.
- Regular/composite ON_CHANGE manifest fixtures exercise public reconciliation,
  restart reconstruction, native API fixtures, retained receipts and no-repeat
  convergence. Reservation actors and manager settlement are simulated.
- Final regular/composite cross-generation race fixtures passed (see final log);
  shared worker authorization/RBAC tests passed (`2.725s`). See the adjacent
  `template-private-crossrevision.log` and `template-private-worker.log`.
- Live authentication from the user-provided credential file succeeded;
  `.100`, `.101` and `.103` were Reachable with software versions `17.18.3`,
  `26.02.1` and `17.18.3` respectively. No CLI deployment was performed.

## Remaining qualification

Installed API-server schema admission, actual worker Secret projection and
mutation leases, complete live parameter-change/rollback cycles, key rotation
baseline recovery, broader multi-target lifecycle, definition creation plus
deployment, and unsupported template variants remain pending. Full model parity
and merge readiness are not established by these fixtures.

## Regression findings and correction

The initial broad race run exposed two regressions (retained in
`template-private-regression-initial.log`): an overly broad template receipt
parser reached shared RMA cleanup, and the value-injection guard rejected older
in-memory snapshots with an omitted use. The receipt extension is now strictly
limited to template deployment resources. Omitted use preserves default value
injection; explicit private comparison use remains prohibited from injection.

The corrected targeted race run passed in `21.912s`, covering LAN injection, all
RMA manifest modes, private comparison/projection and template manifest cycles
(`template-private-fixes.log`).

The final complete race regression passed for API validation, shared adapter and
configuration contracts, Catalyst Center (including its NetAsCode parser) and
Nexus Dashboard. See `template-private-regression-final.log`. Strengthened final
regular/composite fixtures also assert public InSync convergence after a source
generation change; see `template-private-crossrevision.log`. Worker authorization
and Secretless RBAC checks passed, both CRD copies match, strict documentation
build passed and whitespace checks passed. Installed/live mutation qualification
remains outstanding.


## Complete parameter-change and rollback cycles

Regular and composite manifest cycles now qualify the full A-to-B-to-A parameter
sequence on the same committed template version. They inspect all three native
payloads and distinct deployment receipts, reconstruct plans across worker
restarts, pass shared admission fixtures, await strictly increasing verification
timestamps, replace the earlier A receipt only after simulated manager retirement,
and reach public InSync. A further source-generation/formatting change does not
replay the final A deployment. See `template-private-rollback-cycle.log`.

These cycles exposed an integration defect: the ordering guard was originally
in the resource-specific completion callback, while the public template executor
used generic verification. The check now runs in the existing atomic action
receipt writer, covering both paths without a second status store. Its scope is
strictly private template deployments; RMA and ordinary value injection retain
their existing behavior. Prior no-change fixtures and pure policy tests alone
did not establish this property.

The fixtures explicitly model manager settlement and finalizer removal before
reservation deletion. They are not evidence of actual manager/worker actors.
The current local `kind-cisco-vk` API at port 49159 refused connections during
read-only access checks. The authorized `svr-xl` SSH jump host is reachable;
remote cluster readiness is being inspected separately.


Read-only cluster discovery confirmed `svr-xl` and Ubuntu5 are reachable. Ubuntu5
has Ready legacy demo nodes for `192.0.2.24`, `.26` and `.28`, but no resources in
`cvk-live`; it is not the three-switch Catalyst Center qualification environment.
Both saved local development API endpoints (`kind-cisco-vk` port 49159 and
`kind-cvk-dev` port 58432) refused connections. The local Docker daemon was not
running. No demo resources or switch configuration were changed by these checks.


Final affected regression passed: Catalyst Center `283.820s`, shared adapter
`2.334s`, API/configuration contracts, NetAsCode parser and Nexus Dashboard.
See `template-private-rollback-regression.log`. Strict documentation and whitespace
checks passed.

Docker Desktop was started to restore the existing development containers.
`kind-cisco-vk` cannot start its internal containerd CRI service because stored
sandbox metadata is missing; no runtime metadata was deleted or rebuilt.
`kind-cvk-dev` is healthy with its existing older IE3500/IR1101 deployment, not
the `.100/.101/.103` Catalyst Center qualification actors. Those workloads and
the Ubuntu5 oil/gas demo were not modified. Real three-switch worker/projection/
Lease and native deployment qualification therefore remains outstanding; the
observed runtime issue does not prevent further implementation work.
