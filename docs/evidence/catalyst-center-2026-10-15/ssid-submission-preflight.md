# SSID submission freshness and one-use qualification

SSID updates replace the complete native object. The executor now checks the
selected public observation immediately before submission, using the same planner
as initial admission. It binds Global site identity, selected SSID existence/ID,
public fields, sensor/inheritance flags, main-credential presence and unsupported
extensions. The full planner also rechecks ACL existence and OPEN companion
eligibility and compares the resulting action/resource/credential identity.
Unrelated definitions do not invalidate a plan merely by appearing.

The captured observation is copied, including per-priority Multi-PSK metadata.
Returned passphrases remain excluded from public observation and are never reused.
An atomic one-use check consumes the submission closure before preflight. A failed
preflight or uncertain acknowledgement cannot replay that closure. This uses the
existing configuration write and durable journal interfaces, with no new transport,
provider, ND, gNOI, SWIM or foundation API.

## Verification

The complete Catalyst Center adapter regression passed with race detection in
600.395 seconds (1,018 top-level tests in the compiled suite). Strict
documentation build passed in 4.88 seconds, the resource ledger matches all 93
pinned resource types, and whitespace checks passed.

Race-enabled SSID regression passed in 3.211 seconds. New tests refuse selected
appearance/disappearance/replacement, Global site replacement, concurrent public
metadata changes, sensor/extension/credential-presence changes and newly introduced
foreign OPEN companion references. Unchanged observations and unrelated definitions
permit one write. Eight concurrent callers receiving an ambiguous native response
produce one HTTP mutation; all callers receive errors and cannot replay it.

The existing repository Multi-PSK example/public reconciler qualification passed
against Catalyst Center in 19.20 seconds (package 21.108 seconds). Fixture
`CVK_PSK_f5ce7cae2ec65787`, ID
`mdi0q1zl-x1bt-s19M-nwnL-n2nHztjLyZy1nZG3qZbwmuSYxZnq`, remained disabled and
unassociated. Create, accepted-task JSON restart, key reorder without mutation,
stale-projection refusal, same-ID selected-key rotation, no replay and exact cleanup
passed. Exactly one POST, PUT and DELETE were recorded. Cleanup task
`01a1414d-5945-7f16-92f2-cd95158dcfe2` completed, repeated readback proved absence
and original SSID/profile baselines were restored. Final private metadata is at
`/private/tmp/cvk-ssid-manifest-4088531641/` (directory 700/files 600); its journal
has credential applications and no active operation. No generated credentials are
copied into evidence.

## Limits and next work

This is a pre-submit read check, not appliance-side compare-and-swap. An external
writer can still race after the last read. Refusal consumes only this in-memory
plan; durable recovery and uncertainty continue through the existing journal.
Native API/tasks are real, but Kubernetes persistence/restarts are simulated.
Adversarial freshness and concurrent submission are fixture-tested, not induced
by modifying live unrelated definitions. Installed worker/projection/CRD validation,
actual WLC/AP/client authentication, credential-set removal, adoption/pruning and
additional auth/cipher/key combinations remain pending. The generic monotonic
credential guard still refuses removal; this change does not claim full SSID or
NetAsCode parity.
