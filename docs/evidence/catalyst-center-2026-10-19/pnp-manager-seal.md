# Protected manager metadata seal

EnsureMetadataHandoff now writes a staged seal through the existing controller
status CAS. It requires an independently validated product/cohort, exact current
Prepared operation, source/owner/worker observations, the compiled live metadata
policy/binding/namespace and a non-nil read-only runtime compatibility check.
Policy presence alone cannot activate the protocol.

The writer preserves operation and effect history, records the observed worker
ServiceAccount/Pod and canonical cohort, and checks the scope/policy/runtime
again around the CAS. API readback must retain the exact configuration before
the committed seal is returned. If post-commit checks fail, custody is retained;
there is no automatic rollback that could erase uncertain ownership.

A matching existing seal is an observed no-op. A foreign seal is not adopted.
Lost acknowledgement or a CAS conflict requires a fresh authoritative snapshot;
the helper does not retry the old write. Runtime callback arguments are copied
so the callback cannot alter the retained source or cohort.

The existing manager preparation route now has an optional
AuthorizeMetadataHandoff callback. Nil preserves read-only observation. The
composition root supplies no callback, so automatic sealing remains disabled
until compatible retained-fence settlement and worker behavior are available.
No native request, fence acquisition, claim, operation start or release occurs.

## Qualification

Ten controlled modes cover stable/idempotent sealing, copied callback arguments,
missing or denied runtime/policy, post-commit denial, foreign custody,
lost acknowledgement, stale CAS and schema-pruned readback.

The real isolated API run persists source, controller and worker chain, enforces
metadata actor admission, rejects an unbound dry-run seal, then commits through
the impersonated manager. The client simulates loss of that response. A fresh
snapshot recovers the retained seal without another write. Missing compatibility
causes zero writes. Worker readiness, the absent background compiler's status
publication and remaining settlement compatibility are simulated explicitly.
No running worker, retained fence or native effect is qualified.

Final API/seal/actor/bootstrap gates pass 54 test/package events. Seven shared
API, manager, ND and upgrade packages pass 3314 events; selected regressions pass
1049. Main build and formatting/source checks pass.
[Results and fingerprints](pnp-manager-seal.json) record the gates.

## Remaining activation

The current bootstrap fence policy still denies modification/deletion. A
compatible settlement contract, canonical fence acquisition, exact worker claim
and manager release/recovery must be implemented before the compatibility hook
can be supplied. Existing retained-handoff preparation/start/finish barriers stay
in place. Seal creation is not native dispatch authority.

Public new pruning, installed/native lifecycle qualification, full consumer
protection, CR deletion and missing-source finalization, and other pinned
93-resource execution/hardware gaps remain incomplete.
