# Management-address manager declaration application

The private manager bridge now reads the reservation, controller, CiscoDevice
and Node through an uncached-reader contract, checks the correlated completion
and manager settlement with the existing planner, and repeats the shared global
physical-identity/alias and exact old-endpoint binding checks. It requires a
cordoned Node plus an explicit manager authorization callback for source
ownership, authenticated destination identity and continued rollout protection.
The callback receives isolated copies. These requirements do not themselves
supply manager authority or a new endpoint proof implementation.

After authorization, all inputs and bindings are read again. Changed snapshots
are refused. The address-only patch includes a Kubernetes resourceVersion
precondition; conflicts are returned without retry or replay of the native task.
Other device fields and the Node cordon are preserved. No lease is acquired or
released, no maintenance session is settled, and no journal is advanced.

Fixtures cover successful application, denied/missing authorization, an
uncordoned Node, initial/late cross-namespace aliases, stale reservation,
authorization-time device/Node/controller/completion changes, and patch conflict.
The final targeted declaration race suite passed (2.885s), including an assertion that the wire patch contains only the address and metadata version precondition, and refusal to replay a settled old-baseline operation. See [final output](management-address-declaration-final.log). The broader management and
claim-retention race run passed: Catalyst Center 3.895s; shared configuration
4.027s. See [retained output](management-address-declaration-apply.log).

This remains private and is not wired into the installed manager. Kubernetes
cannot atomically update this declaration and the other evidence objects; the
future admitted manager policy must enforce continued protection across that
boundary and verify new worker identity/readiness before restoring scheduling.
Public orchestration, concrete authorization, worker handoff and accepted live
address migration remain pending. No appliance mutation was performed here.

Lab access was rechecked after restoring the dropped local SSH tunnel.
Authentication from the user-provided credential file succeeded; secrets and
token were withheld. Read-only planning passed for all three live switches
(0.95s test; 1.873s package), preserving native UUID/serial/baseline and issuing
no writes. See [live planning](management-address-declaration-live-planning.log)
and [sanitized access output](management-address-declaration-live-access.log).
Fabric/multicast inventory endpoints returned HTTP 200 with zero entries; this
is access evidence, not qualification of a fabric deployment.
