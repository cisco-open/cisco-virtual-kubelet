# Guarded removal of an owned, uncontacted PnP registration

A private native removal primitive now reuses the existing PnP registration
payload, verified Create validator, safe inventory readers, controller transport
and one-use configuration-write guard. A common pure validator preserves canonical
identity/stack checks, and the existing PnP paging boundary carries only safe
projected fields. It does not expose manifest prune/Delete
or claim/reset/device provisioning.

The caller supplies the original verified Create and a mandatory authority
callback. The plan freezes its UUID, registration identity and native snapshot;
it rechecks authority around repeated native reads before a single exact-UUID
DELETE. A denied or uncertain attempt consumes the plan. No native request is
replayed. Imported records and matching serials without a verified Create are
not sufficient.

Eligibility is limited to a single-device registration with exact PID/name/serial,
`Unclaimed` / `Not Contacted`, zero or absent contact times, no reload/inventory
request, no site/workflow/template bindings or stack members, and no task-bearing,
error, unknown or repeated run history. The lab appends one metadata-only audit
entry during registration; that entry is allowed only with explicit false error,
a positive timestamp and no task object. Free-form details and task output are
discarded by a private metadata overlay over the unchanged common identity
projection. Timestamp units are not inferred.
Incomplete managed serial inventories and serial presence in managed stacks
refuse removal. The selected snapshot must stay unchanged through preflight.

The [native API reference](https://developer.cisco.com/docs/catalyst-center/delete-device-by-id-from-pnp/)
describes a synchronous deleted-object response. Acknowledgement checks the
exact returned UUID and any supplied identity; an ID-only object is supported,
consistent with the existing registration primitive. Post-delete state/audit
metadata is not reused as pre-delete eligibility. Separate repeated PnP and
managed-inventory observations are required after valid acknowledgement.
Absence before a validated acknowledgement cannot claim completion.

## Qualification and retained failures

The fresh native cycle passes, with one registration POST, one exact-UUID DELETE,
repeated absence, refusal to replay and a restored empty PnP/catalog network
baseline. The acknowledgement contains the expected UUID and two audit entries,
confirming that deletion itself adds audit metadata. The final selected PnP,
template-reference, SWIM and reload regressions pass (507
passing test/package events); unit/projection checks, five foundation packages
and the main binary build also pass. See [source hashes and results](pnp-uncontacted-removal.json).
An explicit compatibility test confirms that cleanup-only contact/audit changes
leave ordinary PnP dependency snapshots and hashes unchanged. The final private
reader's complete native cycle was requalified after this isolation. ID-only
acknowledgements and post-delete-state variants have fixture coverage; the lab
returned a full deleted object with two audit entries.

The earlier raw Delete response was not retained, so its exact shape and
acknowledgement remain unproven despite the later HTTP 404 observation.

The initial fixture run exposed canonical serial handling in the raw response;
that was corrected before native mutation. The first native cycle registered
`6ad57acd0bc0186771cb1007`, then refused cleanup before Delete because the newly
created object contained the registration audit entry described above. The
subsequent guarded attempt issued one exact Delete, but its response failed the
then-overstrict acknowledgement validation. Read-only investigation confirmed
HTTP 404 for that UUID and an empty PnP catalog. Delete was not replayed, and the
old simulated ownership journal was not converted to successful Delete evidence.
Both native failure logs are retained with the qualification results.

The fresh qualifier uses a generated nonexistent serial, public registration
reconciliation with serialized Kubernetes recovery, and the private guarded
removal. Its transport permits only that registration POST and selected UUID
DELETE, plus read/authentication requests. It forbids physical claim/reset/device
operations. Kubernetes persistence in this qualifier remains simulated.

## Remaining execution gates

This closes a native primitive prerequisite, not `catalystcenter_pnp_device`
parity. Public source pruning and CR deletion still require durable Delete
preparation/acknowledgement/absence receipts, recovery without replay, whole-model
omission policy, actor authority and manager finalization. Installed actors,
stack deletion, contacted records, mixed-state composition, physical claiming
and complete onboarding remain unsupported by this path. The full pinned
NetAsCode model remains incomplete.
