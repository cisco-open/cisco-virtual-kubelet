# Versioned global credential task recovery

Versioned global credentials can now observe a retained, acknowledged `Unknown`
Create or Update without submitting it again. The existing controller journal,
projection resolver and version receipts remain the execution foundation. The
shared credential rotation rule is unchanged: adding a destination was already
supported; removing one remains rejected.

Recovery requires the current declared resource, owner, configuration and source
bindings, original task identity, exact projected versions and valid operation
envelope. Updates also require the prior owned UUID and monotonic version
transition. Metadata-only receipt families cannot cross into this recovery path.
Both observation and final credential receipt publication require the complete
correlated task tree; an unavailable child tree cannot fall back to root success.
Taskless or changed-scope operations remain held.

## Qualification

| Gate | Result | Scope |
| --- | --- | --- |
| Race regression | 27 families / 153 subcases; 11.409s | Credential selection, scope refusal, task trees, source versions and standalone/composed public manifests |
| Private API server | 12 subcases; 19.265s | Six versioned types, standalone and composed, actual Kubernetes persistence; native task service simulated |
| Native Catalyst Center | 13.322s | Generated unused SNMPv3 definition; public reconciler with simulated Kubernetes persistence |
| ND regression | 4.541s | Existing ND test package |
| GNOI/SWIM regression | 6.527s | Existing upgrade-focused controller tests |
| Production build | Passed | Darwin/arm64 binary; not installed |

The native sequence created AUTHNOPRIV with authentication version 1, promoted the
same UUID to AUTHPRIV by adding privacy version 1, then rotated authentication to
version 2. Each of the three acknowledged tasks was deliberately retained as
Unknown and recovered using its original task. Exactly one POST and two PUTs
occurred; restart caused no replay. Status excluded generated secret values.

The unused generated UUID was then deleted under supervised exact-UUID cleanup,
separately from declarative pruning. Its complete cleanup task tree succeeded and
the public global-credential metadata baseline was restored. This does not prove
secret byte equality or successful device authentication. The terminal simulated
journal retains its historical applied receipt; this is not an installed worker
or a qualification of declarative credential deletion.

The first native attempt stopped on an invalid test fixture contract before any
journal or native mutation. An independent read confirmed its generated name was
absent. The fixture was corrected to preserve its selected model contract after
reload, then the final qualification used a fresh unique definition. The initial
failure is preserved separately from the passing run.

Exact final source hashes, binary hash, native task IDs, log hashes and private
checkpoint locations are recorded in the [validation record](global-credential-recovery-validation.json).
Logs: [regression](global-credential-recovery-regression.log),
[API](global-credential-recovery-api.log),
[native final](global-credential-recovery-native.log),
[native initial fixture failure](global-credential-recovery-native-initial.log),
[ND](global-credential-recovery-nd.log),
[GNOI/SWIM](global-credential-recovery-upgrades.log),
[build](global-credential-recovery-build.log).

## Remaining boundaries

This milestone does not qualify installed actor delivery/recovery, physical
credential authentication, metadata-only NOAUTHNOPRIV Unknown recovery,
metadata/versioned family migration, removal of secret destinations, credential
prune or ownership handoff. It does not enable bulk fabric dispatch or prove
complete NetAsCode parity. Existing installed site qualification predates this
binary and must not be attributed to these sources.
