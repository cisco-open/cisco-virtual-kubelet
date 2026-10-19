# Global-only and credential-bearing discovery composition

One Retain-only `inventory.discovery` source can now contain both global-reference
jobs and jobs using projected credentials. Both use the existing shared section
owner, operation slot, transport and reconciliation loop. This closes the row
composition gap recorded in the [public Create stage](discovery-credential-public-execution.md),
not complete discovery or pinned-model parity.

## Reuse and ownership

The credential-aware compiler delegates global-only rows to the existing
`ownedDiscoveryExecutor`. It reuses that executor's selection, native fingerprints,
creator receipts, owned replacement, original-task recovery and completion
callbacks. Resource identities remain unprefixed `discovery-owned:` and
`discovery-record:` keys; existing receipts are not migrated into a new group.
Credential-bearing rows retain their distinct paired creator/version proof.

The combined selector validates both families before choosing a mutation. The
ordinary executor's final dispatch guard repeats the credential-aware preflight
and verifies current global-only Create identity before invoking its existing
guard. Credential submission already reobserves all declared jobs and global
references. A later ownership conflict therefore blocks earlier partial creation.
Unsupported non-secret inline metadata on a global-only row is refused rather
than discarded by delegation.

Removing the last projected credential from a job cannot reclassify its distinct
creator receipt as an unowned global-only import, including when all secret refs
are removed and routing returns to the ordinary compiler. The legacy selector
checks that cross-family boundary. This is a refusal fence, not implemented
credential removal or ownership migration.

## Controlled and Kubernetes API qualification

Six composed-source scenarios run with controlled native responses and again
with real API persistence, alongside the two existing credential-only scenarios:

- Inline and ConfigMap sources create one global-only and one credential-bearing
  job serially, preserve original source-index secret pointers and converge
  across reconstructed workers without replay.
- A configured one-nanosecond timeout exercises the original-task Unknown path
  for **both** ownership families.
- An existing unowned credential job blocks creation of the missing global-only
  job, despite that job being selected first by the ordinary executor.
- Removing projected fields from a job whose remaining public global-reference
  metadata matches cannot silently import it or publish InSync.
- Changing the global-only job uses the existing owned Delete/Create lifecycle:
  one Delete and one additional Create produce a new UUID/task, while the
  credential creator and applied-version receipts remain byte-for-byte unchanged.

The composed example compiles with synthetic projected inputs. Existing public
discovery, credential, generic configuration and API-server gates remain part of
the regression boundary. The [validation record](discovery-credential-composition.json)
contains exact source hashes, commands and terminal results.

## Native scope and remaining gaps

Two actual-appliance read-only cases qualified the original single-job refusal
and the composed refusal. The composed source contains a missing global-only
candidate referencing an existing unambiguous credential definition and the
existing unowned `Switches` job. Three reconstructed reconciles cannot create the
candidate or adopt the existing job. A transport-level product-mutation block is
independent of that assertion. The one-job baseline and all three device
UUID/serial/address identities remain unchanged.

Native persistence is fixture Kubernetes state. No synthetic device credential
was sent to the appliance, no device authentication was attempted, and no
installed-worker rollout or live composed Create/replacement was qualified.
Authorized device CLI/SNMP inputs are still needed for live credential delivery.

Credential-bearing rotation/removal, replacement, prune/delete, broader
inventory/template composition, source-loss recovery and remaining schema/default
coverage remain open. Global-only rows with extra inline metadata and
metadata-only NoAuthNoPriv execution also remain unsupported. The
[93-resource ledger](../../controllers/catalyst-center-resource-parity.md)
continues to record partial coverage explicitly.
