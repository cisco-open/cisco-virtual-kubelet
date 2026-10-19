# Discovery credential input and task verification

Follow-up: the subsequent [public Retain-only execution stage](discovery-credential-public-execution.md)
now connects guarded Create and original-task recovery. This record retains its
original private-stage source and qualification boundary.

This is a private implementation stage on `pr/johalley/catc-support`, not public
inline-credential execution or complete discovery parity. The public Apply path
still refuses these sources before native I/O. No credential-bearing discovery
job was created during this qualification.

## Pinned contract and common components

The pinned NetAsCode module remains **0.5.0**. Field shapes were checked against
the official provider's
[v0.5.11 resource schema](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.5.11/internal/provider/resource_catalystcenter_discovery.go)
and
[v0.5.11 API mapping](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.5.11/internal/provider/model_catalystcenter_discovery.go).
Their exact source hashes are retained in the validation record. This does not
change the module pin or imply complete provider-version compatibility.

The private parser handles CLI password/enable-password sets, HTTP read/write
credential strings, SNMP communities and authentication/privacy passphrases. It
validates known usernames, descriptions, protocols and SNMP mode dependencies.
Plaintext credential values in the model are refused. Secret destinations are
derived from the complete validated model, then resolved through the existing
CVK per-key filesystem projection and alias authorization. No additional Secret
reader, credential store, transport, dispatcher or foundation API is introduced.

Each selected secret leaf uses the existing `{version:N,value:"..."}` envelope
and a companion `<field>_version` model marker. For the two set-valued password
fields, `value` contains a JSON string array: the complete unordered set is
versioned atomically. This is a CVK transport convention, not an upstream
NetAsCode field. Malformed, duplicate, empty, stale and remapped projections are
refused. Original source-index JSON pointers survive canonical job sorting.

Global credential names use the existing unambiguous native ID resolver. Mixed
inputs cannot silently drop a missing global reference or substitute a guessed
ID. Input fingerprints include public intent, selected IDs and credential
version/source labels; they contain neither secret bytes nor hashes of secret
bytes. Fingerprints cannot authorize submission or serve as creator receipts.

## Read-only verification stage

The verifier requires the exact retained Verifying operation, controller and
config identities, exclusive section ownership, source version and credential
bindings. It reads the original task once and validates completion timing and
the resulting discovery UUID from those same bytes. It then checks that UUID
and the selected public fields against complete native discovery inventory.

Native readback intentionally discards every password/community/passphrase
property, including echoed or masked values. CLI username order is canonical;
duplicates and changed usernames are refused. Public SNMP metadata is checked
without comparing write-only credentials. Before returning a candidate, the
verifier rechecks the source, projected material, journal version and global
alias resolution. It neither advances the operation nor persists a receipt.
The returned controller version is a required fence for the future atomic writer.

Controlled cases cover successful inline/mixed observations, pending/failed/wrong
tasks, future timing, missing result IDs, replaced/missing/duplicate jobs, public
metadata drift, changed credential counters, wrong input/action/phase, foreign
ownership, mid-observation journal/source/material changes, replaced global IDs
and native errors. Separate tests verify all selected public metadata and that
credential echoes never enter typed readback. API-backed cases also exercise
persisted sources, ConfigMap identity and operation/version serialization.

The initial observer test exposed timestamp precision differences in the test
fixture; its submission timestamp now uses Kubernetes' persisted second precision.
The failing log is retained alongside the corrected run.

## Qualification boundary and remaining execution work

The existing lab discovery still reports successful CLI, SNMP and NETCONF
collection for all three switches. This was a read-only regression check against
an existing job; it does **not** qualify the new inline-credential delivery path,
a fresh device authentication attempt, or an installed worker rollout.

A subsequent private stage now supplies the [atomic credential/creator receipt
writer](discovery-credential-atomic-completion.md). Public activation still needs
shared submission guards, no-replay restart/Unknown recovery, owned replacement,
credential rotation/removal history, deletion/source-loss handling and live
qualification. In particular, an accepted request or matching metadata cannot
establish credential application or successful device authentication. Existing
global-reference discovery ownership and cleanup remain separate and unchanged.
Authorized device CLI/SNMP credential input is still needed for the new live flow;
the Catalyst Center API account is not assumed to be a device account.

The earlier optional-null full race audit passed 13,627 test/subtest events, but
predates this private credential stage. See its explicit
[source boundary](discovery-null-full-suite.json). Current focused tests, source
hashes, build and native-reader results are recorded in
[the validation record](discovery-credential-contract.json).
