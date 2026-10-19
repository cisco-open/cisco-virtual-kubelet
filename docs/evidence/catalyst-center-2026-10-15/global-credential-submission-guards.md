# Global credential submission guards

The shared global credential executor now rechecks every selected definition in
two fresh native metadata observations immediately before submission. A changed
UUID, removed definition, changed metadata or newly occupied selected name blocks
the request. Changes to unrelated definitions are allowed. The observation is
frozen independently of the executor's mutable selection. No secret bytes or
secret digests enter retained evidence.

Each planned request permits one submission attempt. A missing acknowledgement
does not permit replay through the same closure. This complements the common
configuration journal's source, projection, ownership and recovery checks; it
does not provide a distributed transaction against concurrent appliance writers.

## Validation

- Global credential race tests, including public manifest create, rotation and
  restart: passed, 6.296 seconds.
- Affected global/site credential, combined network and SWIM race tests: passed,
  6.600 seconds.
- Native `TestLiveGlobalCredentialLifecycle/cli_credentials`: passed, 8.54
  seconds (package 10.687 seconds). One unassigned credential was created,
  independently rotated, read back and checked for converged no-op. Its exact
  native UUID was deleted and original credential identities were retained.
- Native fixture UUID: `197b6cc7-93a9-47b1-9170-1349c234ad96`.
- Create task: `01a13fde-269c-7cdb-8a7c-be4daf4f726b`.
- Update task: `01a13fde-2d95-7294-b345-a5dd887f81fb`.
- Cleanup task: `01a13fde-378e-783d-8f5c-7023b0282a16`.

Native qualification used the API with simulated executor receipts; it did not
exercise installed Kubernetes actors or device authentication. Other credential
kinds share the guard but were not newly qualified against the appliance in this
run. See the adjacent test and native logs.

## Remaining work

At this checkpoint SNMPv3 `NOAUTHNOPRIV` execution still needed metadata
ownership in the existing action journal. That lifecycle was subsequently
implemented and native-qualified; see [the manifest qualification](snmpv3-noauth-manifest.md)
for its evidence and remaining installed-actor, service and cross-mode limits.
Credential pruning remains separate unfinished work.
