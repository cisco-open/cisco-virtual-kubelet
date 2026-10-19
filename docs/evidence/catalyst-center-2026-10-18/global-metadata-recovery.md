# Metadata-only global credential task recovery

SNMPv3 NOAUTHNOPRIV now recovers an acknowledged Create/Update retained as
Unknown through the existing global credential plan and journal. Selection binds
the exact declared non-secret payload, owner, configuration and original task.
An Update requires the prior metadata receipt and its exact native UUID; a Create
requires no prior receipt. Duplicate declarations/receipts, password-backed
receipts, changed payloads, missing tasks, foreign ownership, future submission,
prepared fields, SSID history and credential-version fields cannot grant recovery.
The public reconciler separately retains its source/generation checks.

The metadata-only path now observes the complete correlated task tree before
Verifying, with no root-only fallback. Existing repeated native metadata/UUID
readback and task-tree verification at atomic action-receipt publication remain
in place. No new writer, Secret lookup, transport, CRD or receipt-family conversion
was introduced. Unacknowledged outcomes remain fenced rather than replayed.

## Qualification

| Gate | Result | Scope |
| --- | --- | --- |
| Race regression | 13 families / 149 subcases; 11.313s | Metadata recovery selection, complete/failed/running/foreign/malformed/unavailable trees, public create/update/drift repair and existing credential ownership/versioned paths |
| Private Kubernetes API | 2 families / 12 subcases; 25.880s | Actual metadata journal/status persistence plus all six versioned standalone/composed recovery variants; native task service simulated |
| Native Catalyst Center | 15.579s | Generated unused metadata-only credential; simulated Kubernetes persistence |
| ND | 4.601s | Existing ND regression package |
| GNOI/SWIM | 6.332s | Existing upgrade-focused controller tests |
| Production build | Passed | Darwin/arm64 cisco-vk; not installed |

Native qualification created one unused NOAUTHNOPRIV definition, retained its
acknowledged task as Unknown, then recovered and verified its UUID. It updated
the username on that same UUID and recovered the second original task. Exactly
one POST and one PUT occurred. Fresh reconciler instances performed no-op checks
without additional writes. The test allowlist applies to renewed authenticated
sessions too, and a private checkpoint records native identities and journal
transitions without API credentials.

Supervised exact-UUID cleanup then deleted only this generated definition. Its
complete task tree succeeded and the full non-secret credential metadata baseline
was restored. The terminal simulated journal retains its historical applied
receipt; this fixture is not an installed worker. Cleanup is separate from
manifest-driven pruning and does not establish declarative credential deletion.
No credential was assigned to a site or device and no physical authentication was
tested.

See the [source hashes, task IDs, checkpoint and gate record](global-metadata-recovery-validation.json),
[regression log](global-metadata-recovery-regression.log),
[API log](global-metadata-recovery-api.log),
[native log](global-metadata-recovery-native.log),
[ND log](global-metadata-recovery-nd.log),
[upgrade log](global-metadata-recovery-upgrades.log) and
[production build log](global-metadata-recovery-build.log).

## Remaining boundaries

Installed metadata-only recovery, physical device authentication,
NOAUTHNOPRIV-to-versioned receipt-family migration, credential removal/prune and
ownership handoff remain pending. This closes the metadata-only Unknown gap
recorded in the preceding [versioned recovery milestone](global-credential-recovery.md);
that milestone's source hashes and qualification boundaries remain historical.
Full pinned NetAsCode execution parity is not established by this change.
