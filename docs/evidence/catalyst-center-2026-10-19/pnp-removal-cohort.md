# Whole-cohort PnP removal preflight

The private source-prune planner now validates all remaining owned registrations
omitted from the complete pinned PnP-only inventory before selecting one Delete.
It requires canonical creator baselines and intact ownership, rejects malformed
or orphaned tombstones, budgets all remaining absence receipts in the existing
count/byte-bounded ledger and sorts the pending UUIDs deterministically. Legacy
registrations without canonical cleanup evidence cannot be guessed or adopted.
Declared serials remain retained; completed removals are excluded only by their
original verified, creator-bound absence receipts.

The existing native eligibility and managed-serial validator is shared by the
single-record reader and cohort observations. Every pending record must still
match its baseline, immutable registration identity and uncontacted/unclaimed
eligibility. The same source/owner/journal authority brackets observe the cohort
again at dispatch, including inside the lower native guard. A late sibling
change can refuse the first Delete. The existing one-use pre-HTTP proof can then
retire only the exact unchanged Submitting operation, preserving all creators;
a fresh cohort plan is required. Accepted or ambiguous native outcomes retain
the existing fence. No new transport, lock, status schema or public permit exists.

Each successful Delete retains its independent atomic absence receipt before
release. The next record requires a fresh plan from current durable state;
there is no retained batch mutation token. This is not an atomic multi-record
transaction and cannot prevent external appliance changes between observations.

## Qualification

Controlled and actual Kubernetes API cases cover ordered two-record cleanup,
restart between operations, a retained desired sibling, duplicate serials,
legacy cleanup evidence, count/byte capacity, malformed tombstones, a managed
sibling and attached content appearing before dispatch or in a later native-read
bracket. Their final results and source fingerprints are in
[the qualification record](pnp-removal-cohort.json).

The corrected controlled/real-API matrix passes 95 test/package events; selected
PnP/journal/upgrade regressions pass 1032 events. Six foundation packages, the
main binary build, formatting/93-row ledger checks and strict documentation
build pass.

The native test created two generated nonexistent-serial registrations, retained
their original creators and canonical baselines, and omitted both from intent.
A process-local read refusal before HTTP Delete retired with zero Deletes.
Fresh planning then completed two exact UUID Deletes in order, each through
acknowledged Verifying, serialized restart, independent absence receipt and
receipt-gated release. Two original creator rows and two absence rows remain.
The full PnP catalog and network baseline were restored. The transport allowed
only the two exact generated POST payloads and their verified UUID Deletes;
no physical device or claim/reset operation was performed.

Native Kubernetes persistence is simulated JSON; actual Kubernetes API tests
run separately. After native qualification, only the cohort fixture test changed
to correct a tombstone that the CRD rejected before execution and to extend
negative coverage. Production and live qualification sources are unchanged.

## Remaining scope

New public dispatch still requires the existing manager-owned global identity
and reservation checks with appropriate actor access. Full native consumer
census, missing-source and CR deletion context, manager finalization and installed
actors remain incomplete. The public acknowledged-recovery path remains enabled
without granting new dispatch or whole-model convergence. Full support for the
pinned 93-resource NetAsCode model remains incomplete.
