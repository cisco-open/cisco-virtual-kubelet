# LAN automation link dependencies

## Implemented execution prerequisites

The pinned 0.5.0 `lan_automation.links` model and wire mapping are preserved.
New dependency observation combines exact endpoint interfaces, physical topology,
recorded discovery origins, same-site IPv4 LAN pool identity and the existing
repeated native/managed CiscoDevice/Node binding. No new transport, framework,
status store or RBAC permission was added.

The [2.3.7.6 physical topology API](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/get-physical-topology/)
returns a complete object with node and link arrays. Its device IDs, port IDs and
port names are bound to the existing native interface reader. The API documents
no pagination; the reader refuses HTTP 204/206 and missing/null arrays instead of
inferring absence. Compound topology IDs remain opaque. Unknown state text is
preserved rather than interpreted as readiness.

Binding accepts either link direction and rejects a competing neighbor, duplicate
adjacency, reused/replaced port ID, abbreviated or mismatched port name, greyed
node/link or mismatched management IP. Unrelated links on other ports do not
change the selected footprint. Native link absence is observation only; it is
not free-port, deletion, ownership or completion proof.

The [LAN device update API](https://developer.cisco.com/docs/catalyst-center/2-3-7-7/lan-automation-device-update/)
requires the add-link pool to come from the endpoints' discovery site. Discovery
origin is therefore resolved by recorded physical serial and management address,
not current inventory placement or a session seed address. All agreeing historical
origins are retained; conflicting sites/address aliases and absent history remain
unresolved. STOP/status text is retained as metadata and cannot prove safe session
completion.

Same-site IPv4 LAN pool binding reuses the existing complete IPAM reservation and
global-parent validation. Its identity, owner and parent digest are retained with
the discovery scope. Cross-site/shared visibility, available capacity and session
readiness need additional qualified semantics. These dependency readers grant no
execution authority. A delete with no model pool invents no pool dependency;
delete ownership/admission remains required.

The combined observer brackets pool/session reads with exact adjacency readback,
then uses the existing repeated native/managed group resolver. No partial footprint
survives a pool, origin, native endpoint or managed identity race.

## Fixture validation

The complete focused LAN race suite passed in 3.966 seconds. It includes existing
models, native payload mapping, sessions, interfaces and identity readers plus:

- complete/empty/partial/malformed topology and unknown state handling;
- forward/reverse exact adjacency and competing or duplicate neighbor refusal;
- replaced links, ports, managed peers and interface configuration;
- discovery origin loss, serial/address aliases, conflicting sites and seed-only history;
- reservation/owner/global-parent changes and missing/non-LAN pools;
- combined pool/origin changes between reads and topology changes during pool observation;
- unchanged footprints, order-independent records and pool-free delete prerequisites.

See `lan-link-dependencies-focused.log`. Native HTTPS and Kubernetes authority
are fixtures in these tests, not installed worker/Lease qualification.

## Live qualification and remaining work

Read-only authenticated lab qualification binds and rereads both existing links
among the three authorized switches. The native topology has three device nodes
and two links. Existing link observation does not assert that LAN automation
created or owns those links.

Current LAN session history is empty. Origin-dependent pool resolution therefore
remains held. No link, session, pool or device configuration is changed by the
qualification. See `lan-link-dependencies-live.log` for the final result.

Actual add/delete execution remains incomplete: pool capacity/shared visibility,
active-session readiness, physical group admission/dispatch, durable task and
ownership/recovery, independent convergence, public Report/Apply composition,
installed worker/manager actors and accepted native execution all need completion
and qualification. Read-only prerequisites are not full resource parity.


## Final regression

The final complete affected race suite passed for Catalyst Center, its NetAsCode
parser, shared controller-configuration contracts and Nexus Dashboard. See
`lan-link-dependencies-regression.log`. Strict documentation and whitespace checks
also pass. These results establish dependency binding and read-only lab behavior,
not accepted LAN link execution or full NetAsCode parity.
