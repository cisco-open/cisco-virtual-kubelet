# Opt-in bulk-site manifest execution

Bulk creation is enabled with the manifest annotation
`cisco.vk/catalyst-center-bulk-sites: "true"`. This transport option stays outside
the resolved NetAsCode stripe, as upstream `use_bulk_api` does. Absent/false
preserves singular execution; invalid values are refused. The repository example
is `examples/configs/catalyst-center/sites-bulk-apply.yaml`.

The public site executor retains the original missing-member bitset and
pre-existing declared-site/ancestor UUID digest in the existing journal resource
field, anchored to the native Global UUID. It reuses global section ownership,
Prepared/Submitting/Accepted/Verifying/Verified transitions and task receipts.
Immediately before submission it checks fresh native membership, the exact
owned Submitting record, source/config/controller identity, root UUID and
absence of incompatible credential/physical/dependency envelopes. No shared
schema, transport, actor permissions, ND or upgrade routing was changed.

Accepted bulk tasks use the existing task-tree observer. Native readback must
match every declared member and attribute before verification. Partial results
retain Verifying and the original selection; later native convergence completes
without a new mutation. Failed tasks retain Accepted, lost receipts retain
Unknown and changed source retains the old operation. Disabling bulk transport
does not prevent retained bulk verification. Existing divergent resources use
ordinary UUID-bound updates, because the bulk API skips existing sites.

Public fixtures pass across simulated worker restarts for complete and partial
batches, late convergence, failed tasks, lost receipts and changed source.
The complete fixture then updates floor width through the manifest, reaches
InSync and confirms no batch replay. Direct-dispatch guards refuse missing or
Prepared-only journals, lost ownership, changed source, wrong root and paused
controllers. Final targeted race regression: 4.308s.

The broader race suite passed after initial public integration: Catalyst Center
240.353s, ND 4.751s, shared configuration 31.496s, controllers 34.513s and CLI
	10.051s. Subsequent dispatch guard hardening and the public update case passed
the final targeted suite above. See the retained
[broader regression output](site-bulk-public-regression.log).

The final hardened public reconciler ran against the real lab API using
**simulated Kubernetes persistence and worker restarts**. It created
`Global/CVK-Q-20261014-145002-000000000`, its building and floor, reached InSync,
cleared the operation and retained the owner. The native binding and task tree
were independently checked, followed by a normal native floor update, no-op and
verified cleanup. Live test 14.11s, package 14.981s. The subsequent floor update
was native, not a live public manifest update. See the retained
[live reconciliation output](site-bulk-manifest-live.log).

Installed manager/worker rollout, real API-server admission, live public updates,
larger batches, wider field/API/default combinations and ownership-aware
update/delete semantics remain pending. No full resource or section parity is
claimed.
