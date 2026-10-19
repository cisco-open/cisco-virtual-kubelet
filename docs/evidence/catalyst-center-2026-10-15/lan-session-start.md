# LAN session launch implementation and qualification boundary

Pinned model: NetAsCode Catalyst Center 0.5.0, commit
`a435555fe828f03cf3311d52fe1ab6572a247916`, `cc_lan_automation.tf`.
This extends the existing START parser, credential destination mapping and native
dependency readers with a private launch preflight and one-use execution primitive.
It does not advertise public or native-qualified LAN session creation.

## Implementation

`lanSessionPreflight` uses the existing authenticated client, complete inventories
and physical identity helpers. It observes an idle enabled LAN service, binds
seeds/primary ports/site/pool identities, and reads exact reservation-bound usage.
The initial visibility contract admits same-session-site IPv4 pools only. It
requires conservatively remaining counts of at least the declared physical device
count in the principal pool and two addresses in an explicit link pool. These are
necessary lower bounds, not proof of contiguous ranges, topology-dependent demand,
DHCP availability, seed needs or successful allocator admission.

PnP binding requires an explicit complete physical set, including all stack
members, excludes devices already in managed inventory, and requires Unclaimed
state. Authentication/workflow metadata is retained rather than interpreted as
successful admission. Dependencies, PnP identity/state, native activity and pool
usage are re-read within the observation bracket; a later planner comparison binds
the entire nonsecret observation. Its hash explicitly projects private dependency
fields so empty-struct JSON serialization cannot discard their commitments.

`planLANSessionStart` uses the existing pinned array payload and the documented
[V2 START endpoint](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/lan-automation-start-v2/).
It freezes source slices/fields and ephemeral projected credentials, requires a
version for nonempty password material, and generates `lan-session-start:v1:`
identity from metadata/version plus the observation digest. Secret bytes do not
enter that identity. Submission consumes the plan atomically, revalidates native
evidence before and after its mandatory authorization callback, sends one POST
and accepts only the documented qualified HTTP/task-ID shape. Denial, stale state,
lost/malformed acknowledgement or an unqualified accepted status consumes the
plan and prevents same-plan retry. Returned task URLs are never followed.

The callback is an integration boundary for the existing group/journal authority,
not an implemented grant: the eventual caller must establish fresh source/owner,
authentication/seed readiness, allocator and discovery-domain eligibility,
all-member reservations and durable Submitting state. Cross-process no-replay,
ownership receipts, STOP/completion and reservation settlement remain unimplemented
for sessions. A native task ID alone does not establish completion.

## Tests

The [focused fixture races](lan-session-start-focused.log) passed in **3.164s**.
They cover active/disabled service, exhausted/changing counters, foreign pool,
missing/claimed/already-managed PnP targets, unbounded discovery, replaced seed,
port/PnP IDs, changed authentication/workflows, immutable caller inputs, denied or
missing authority, changes during authority, lost/malformed/202 receipts and
same-plan retry. Concurrent consumers permit only one callback/request, and
credential-version changes affect the resource identity while secret bytes do not.
These are native HTTP fixtures with simulated authority, not actual journal,
reservation actors, physical device authentication or hardware automation.

## Current native environment

The [read-only run](lan-session-start-live.log) passed in **12.385s**, body **10.25s**:
maximum 5 sessions, active 0, managed devices 3, PnP devices 0, LAN pools 0 and
complete usage observations 0. No explicit isolated declaration was supplied;
`DeclaredLaunch` was explicitly **SKIP**, not launch-preflight qualification.
The live test has no path that submits START, STOP or reset, even when supplied
an external metadata-only declaration via `CATC_LAN_SESSION_MODEL`.

## Required remaining work

Cisco documents that devices outside the declared list discovered through DHCP
may be reset and reloaded at the end of the process. The list is therefore not
proof of complete physical containment. An enrolled and isolated discovery domain
and authorization/admission for every possible affected device are prerequisites
for enabling public execution; upgrade approval for existing managed switches
does not establish that containment.

Shared/inherited pool visibility, complete allocator capacity/contiguity semantics,
discovery authentication, seed eligibility, projected-secret application receipts,
all-member physical admission, durable start/stop ownership and recovery, actual
completion verification, public Report/Apply wiring and installed/native launch
qualification remain open in the [ledger](../../controllers/catalyst-center-resource-parity.md).
No ND, direct-device upgrade or SWIM logic changed.

## Regression checks

The [shared foundation/ND checks](lan-session-start-foundations.log) passed from
cache. [Manager/worker checks](lan-session-start-runtime.log) passed in **4.019s**.
[Catalyst Center SWIM fixtures](lan-session-start-swim.log) passed in **2.471s**.
[Upgrade path checks](lan-session-start-upgrades.log) passed: software lifecycle
**1.211s**, software-upgrade provider **14.546s**, direct IOS XE gNOI **5.297s** and
gNOI runtime **5.336s**. These verify their respective tests, not live device
upgrades. Strict documentation build passed in **4.33s**; whitespace checks passed.

The final [LAN/PnP race regression suite](lan-session-start-related.log) passed in
**221.875s**, covering the new launch tests and existing LAN link/session/PnP tests.
Production code was unchanged during that run. Full native launch and installed
qualification remain unproven, as recorded above.
