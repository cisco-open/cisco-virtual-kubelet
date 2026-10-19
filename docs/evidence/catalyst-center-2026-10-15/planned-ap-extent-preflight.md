# Planned AP extent preflight

## Implementation

Planned AP execution now checks X/Y and an explicitly selected Z against floor
extents in feet. Negative coordinates, nonnumeric/nonfinite values and coordinates
beyond the applicable width/length/height are refused. Optional Z stays omitted;
no default or controller-specific minimum mounting height is invented.

Declared floor dimensions use the same three-decimal normalization as existing
site submission. Meter dimensions are converted using 0.3048 meters per foot;
placement coordinates are never themselves reinterpreted or converted. Native
floor readback requires a floor identity and complete finite positive dimensions
with known feet/meters units. Conversion overflow is refused.

The enclosing sites planner checks all declared positions against the intended
future floor geometry before exposing any earlier geometry mutation. Existing
placements also validate their complete merged update payload, including retained
Z. A valid expansion is therefore allowed to proceed before placement, while
invalid positions cannot create earlier hierarchy resources.

Before placement POST, frozen effective coordinate snapshots for the complete
selected population are checked against a fresh native site inventory and exact
original floor UUIDs. Missing/replaced floors or shrinking extents, including a
later target's floor, refuse dispatch. Every attempted closure is consumed on
refusal, missing acknowledgement or success. Verification also requires the
complete merged payload to remain within current native extents.

These are bounds checks through the existing Catalyst Center compiler, transport
and configuration journal. They do not add dispatch authority, atomic native
snapshots, new foundation/CRD structures, or an uncertain-operation reset path.

## Local verification

[Affected race suite](planned-ap-bounds-affected.log): **10.399 seconds**, covering
Site, PlannedAP, FloorImage and SWIM/Swim cases.

New fixtures cover feet/meters, each axis, zero/exact-boundary/negative/above-bound
and nonfinite values, required X/Y versus optional Z, submitted numeric precision,
zero-rounded dimensions and missing native fields. Complete-source refusal is
checked before AP reads or geometry writes. An existing narrow floor can expand
before the new point is placed. Dispatch cases cover selected/later floor shrink,
UUID replacement, missing dimensions, caller mutation of the original position,
missing acknowledgement and no replay.

[Additional verification race cases](planned-ap-bounds-verification.log),
**2.229 seconds**, refuse a selected-value match outside a shrunk native floor
and an out-of-bounds retained Z that the current manifest does not select.

## Native qualification

[Live native run](planned-ap-bounds-native.log): test **38.26 seconds**, package
**40.311 seconds**.

On a fresh isolated hierarchy, compiled sites execution refused out-of-bounds
X, Y and Z using real floor readback. A transport mutation counter remained
unchanged for each refusal. The valid AP9130I/5-GHz/Z=4-foot create and X update
then passed task/readback, public manifest InSync, simulated worker restarts,
no-op and exact observed AP/radio identity/state preservation. Exact planned AP
UUID deletion restored the empty-floor baseline; the temporary hierarchy was
also deleted and independently verified absent.

Kubernetes persistence/worker restarts are simulated; native appliance requests
are real. No operational AP or switch configuration was changed. No new diagnostic
hierarchy was retained by this run. The four older diagnostic scopes remain as
recorded in [prior native qualification](planned-ap-native-qualification.md).

## Remaining limits

The lab previously rejected Z=2 with a minimum Z=3-foot rule. Generic extent
validation deliberately does not claim to discover that controller-specific
minimum or generalize it to all releases. Native validation can impose tighter
requirements than physical extents. Capability-aware minimum-height handling,
optional/default and empty-radio qualification, broader model/radio combinations,
radio replacement, installed actors and public prune/delete remain incomplete.
Exact-boundary native rounding and concurrent changes after the last observation
also need qualification. Full NetAsCode parity is not complete.
