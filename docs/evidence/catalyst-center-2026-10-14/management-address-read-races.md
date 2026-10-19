# Retained worker-read snapshot consistency

Review found that retained address recovery validated live worker objects after
collecting its device/Node snapshots, but could return those earlier snapshots
without checking whether the Node or namespace-wide physical declaration set
changed during the worker reads.

The retained-binding helper now repeats the existing managed resolver after
worker and reservation checks. This includes physical alias detection across
namespaces and exact old-address/UID checks. It requires unchanged device and
Node incarnations/resourceVersions and returns the fresh copies only after
the full observation succeeds. No partial evidence is returned on failure.
No shared identity rule, schema, permission or execution path was relaxed.

New fixtures inject a Node change and a newly created cross-namespace physical
alias exactly during the worker Pod read. Both are refused. The combined race
run passed for all management-address native/managed/recovery fixtures and
single/group claim retention: Catalyst Center 3.870s and shared configuration
3.848s. See [retained output](management-address-read-races.log).

This strengthens the private recovery observer; it does not enable public
dispatch, perform worker rollout or authorize lease settlement. No lab mutation
was performed. Full managed migration and accepted live qualification remain
pending.
