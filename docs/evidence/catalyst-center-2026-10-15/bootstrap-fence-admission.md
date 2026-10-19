# Bootstrap fence admission protection

## Added behavior

The chart now renders a separate ValidatingAdmissionPolicy and Deny binding when
managed topology is enabled. It reserves the `cvk-bootstrap-` Lease prefix and
also matches bootstrap labels, annotations or finalizers on either the old or
new object. This catches marker removal and malformed names. Topology-disabled
installations render neither resource. Existing managed admission policies,
compiled digests, reservation CRDs, worker roles and device/SWIM lease protocols
are unchanged.

Only the configured manager service-account username may create retained evidence
in the shared namespace: `config.leaseNamespace`, or the Helm release namespace
when empty. This differs from the existing per-device namespace fallback for
ordinary device leases; bootstrap serial arbitration must not split by tenant.
Every future manager must corroborate the same configured namespace.

Creation requires the CVK serial-hash name, binding/retain markers, evidence
finalizer, no owner references and a bounded controller-bootstrap holder digest.
The API must not carry expiration, renewal, transitions or coordinated-election
fields. All updates and deletes are refused, including those by the manager.
This intentionally provides no release, expiry takeover or native authority.
The manager-side helper continues to validate the complete source/native/worker
binding; admission checks the creation actor and retained Lease shape, not the
JSON binding's semantic ownership or a controller's product API permissions.

The new prefix replaces the private kernel's generic `bootstrap-` prefix before
any production bootstrap fences have been created. It avoids reserving generic
Lease names used by unrelated applications. Controller and tenant identity remain
excluded from the serial hash, while the retained holder binds their full source
and connector incarnation.

## API-server verification

`TestBootstrapFenceAdmission` installs the rendered policy/binding into a
separate disposable Kubernetes 1.35 envtest API server. Impersonated callers have
broad test-only RBAC so policy rejection cannot be confused with missing RBAC.
The exact helper-produced candidate is accepted for the manager. Worker create,
wrong namespace, missing markers/finalizer, wrong names, owner references,
expiry, foreign holders, marker stripping, manager/worker update and
manager/worker delete are rejected. The retained object is unchanged. Ordinary
leases remain creatable/deletable. No policy type-check warnings were reported.
This test passed in 4.77 seconds (package 6.724 seconds).

The full chart still matches all existing compiled admission contracts, including
negative runtime tests for missing bindings, modified CEL, stale policy and wrong
manager: 0.68 seconds (package 3.559 seconds). Existing manager admission tests
passed in 3.681 seconds. The shared suite passed in 33.306 seconds; provider
reservation, ND and pinned model tests passed from cache. Helm lint for Kubernetes
1.35 passed; topology-disabled rendering contains no bootstrap policy.

## Lab recovery and native requalification

The tunnel recovered. Token, PnP and network queries returned HTTP 200 with one
retained PnP record and three network devices. Exact receipt-based cleanup removed
UUID `6ad123500bc0186771caafae` and verified empty PnP inventory and the unchanged
observed network baseline: 3.39 seconds (package 5.490 seconds). Private retained
evidence remains at `/private/tmp/cvk-pnp-registration-1691719004/`.

A new isolated native Prepared qualification then passed in 4.86 seconds
(package 6.666 seconds). Generated serial `CVK46C46338` received UUID
`6ad125750bc0186771caafe0`; Prepared operation was
`7ddce677a6f25e169f394516b54a749c`. Serialized journal reconstruction matched the
source-bound bootstrap identity. Uninstalled runtime refusal occurred before any
Kubernetes write or native grant; denied claim admission retained the journal
without a site-claim POST. Exact uncontacted fixture cleanup restored empty PnP
inventory and the network baseline. Private evidence is at
`/private/tmp/cvk-pnp-registration-2455916252/` (directory 700, files 600).

This native test uses simulated Kubernetes state. The real API-server policy test
is separate; neither attests an installed end-to-end lab bootstrap runtime. No
site claim, reset, upgrade or other physical device operation was submitted.

## Remaining requirements

The chart protection has not been installed into the lab cluster. The manager
still has no production bootstrap runtime authorizer. Production manager wiring of policy/binding live
verification, connector installation/actor authority, enrollment and rollout
exclusion, one-use dispatch grant, cancellation/settlement/release, public claim
execution and correlated native workflow/template completion remain pending.
Installing only this policy must not enable claiming. It cannot arbitrate new
device declarations or unknown hardware aliases on its own. Namespace teardown
will retain these finalized objects until an explicit settlement protocol exists.
Full PnP and full NetAsCode execution parity remain incomplete.


A subsequent [runtime protection binding](bootstrap-protection-runtime-binding.md)
now directly verifies the complete live policy/binding and binds their
incarnations into every new private reservation. Production actor/enrollment
compatibility, lab installation, dispatch and settlement remain pending.
