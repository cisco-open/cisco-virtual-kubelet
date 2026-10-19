# Owned LAN ADD result observation

The private complete-source ADD planner now recognises an exact owned result
before applying the empty-interface baseline required for a new ADD. The existing
atomic group receipt records `lan-link:<logical>:<result>`. The admitted operation
and physical-group identity remain unchanged; no CRD, transport, journal or
reservation mechanism was added.

The result hash binds the original immutable recovery dependency projection and
both native interface configurations, including allocated IPv4 addresses/masks,
routing mode, ISIS metadata and VLAN fields. Known up/down case differences are
normalised. Pool usage, discovery allocated-address lists and topology cache IDs
do not alter this result identity. Positive native subnet/interface checks still
run on every observation.

Skipping a new write requires one valid receipt, matching configuration ownership,
controller generation and source UID, original device/port/pool/discovery identity,
and the exact retained CiscoDevice/Node footprint. A newer configuration/source
version may add another link without invalidating an otherwise unchanged owned
link. Every unexecuted ADD still receives the complete native preflight. Missing
ports or unsupported actions cannot be silently skipped.

Changed allocations, routing or managed identity hold. Duplicate receipts, foreign
or absent ownership, and legacy receipts without a result hash hold. Existing
addressed ports cannot be adopted. Historical task records are not queried to
recognise an owned result; no fresh ADD capacity threshold is imposed on it.

Receipt replacement remains subject to the existing predecessor-retirement
contract. A different result for the same logical link cannot silently append
another receipt or overwrite the old one. Independent proof is required for
initial publication. A historic receipt plus current native observation suppresses
a repeated write; it does **not** attest current independent device convergence.

## Validation and limits

Race-enabled native HTTPS/fake Kubernetes fixtures cover unchanged results,
exhausted-after-completion pools, topology refresh, unavailable historical task
evidence, changed allocated addresses and routing, legacy/duplicate receipts,
missing/foreign ownership, replaced Nodes, later source generations, and a second
physical link in the same manifest with valid or missing prerequisites.
Observation makes no additional native write and does not release either claim
or change the Verified journal's settlement state.

The focused LAN race suite passed in **22.723s**; see
[focused log](lan-link-owned-focused.log). Fresh live read-only qualification
passed in **5.551s**: maximum five sessions, zero active, three topology nodes,
two links and both adjacencies qualified. Discovery history is still empty;
see [live log](lan-link-owned-live.log).

The complete affected race suite passed: Catalyst Center **309.399s**, plus
NetAsCode parsing, shared controller configuration and Nexus Dashboard (cached
passes). See [regression log](lan-link-owned-regression.log). Strict documentation
build and whitespace validation also passed.

This remains private planning. Public new-write activation, installed independent
device proof qualification, installed actor/Lease settlement, native accepted LAN ADD,
legacy receipt migration/adoption and explicit delete execution remain pending.
Read-only lab prerequisites cannot qualify an owned mutation which has never run.


The subsequent [device-worker proof bridge](lan-link-device-proof.md) provides
the concrete verifier for retained Verifying work. Its installed lab execution
remains unqualified; this does not change the limitations of the earlier tests.
