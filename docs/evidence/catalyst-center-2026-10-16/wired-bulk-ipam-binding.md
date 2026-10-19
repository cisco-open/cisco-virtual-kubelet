# Private wired bulk IPAM identity binding

The private wired provisioning lifecycle now binds declared global pools,
site reservations, their native UUIDs, address parameters, parent pools and owning
site ancestry through the existing typed IPAM inventory and allocation-graph
validator. Two observations must agree. Missing allocations, ambiguity, changed
parameters and identically named native replacements retain the fence.

A private `wired-bulk-provision:v2` resource identity incorporates this dependency
digest. Sources without allocation context keep the existing v1 identity. No
common operation schema, ND provider or SWIM path is changed. Observers propagate
through native/managed planning, one-use dispatch, task verification, retained
recovery, device evidence collection and receipt publication. A v2 operation cannot
be verified without its original dependency context. Final receipt authorization
rechecks both the allocation and the exact independently proved task completion.

Local HTTPS and simulated Kubernetes lifecycle tests cover provision/reprovision,
missing/replaced allocations before and during dispatch, changes after acceptance,
changes during independent proof and final authorization, malformed observers,
missing recovery evidence and preservation of legacy identity. These tests do not
establish installed-worker or rendered-device qualification.

Catalyst Center connectivity was checked using credentials read privately from the
local credential file. Token generation succeeded; read-only inventory returned
C9K-1 (.100) and C9K-4 (.103) reachable on 17.18.3, and C9K-2 (.101) reachable on
26.02.1. No native mutation was performed for this connectivity check.

Public bulk execution remains unavailable. Credentials/assignment dependencies,
full device convergence, installed actors and accepted live bulk mutation remain
pending. The separate installed-cluster API-only recovery still awaits approval.
All parity flags remain partial.

Validation: `go test -race ./internal/controlleradapter/catalystcenter -run '^TestWiredBulk' -count=1` passed (31.395 seconds), including the two final-authorization regressions that failed before the guard was added. See the [regression log](wired-bulk-ipam-regression.log).

Existing external credential assignments now also have [private dependency identity support](wired-bulk-credentials.md); owned definitions and version receipts remain a separate pending requirement.
