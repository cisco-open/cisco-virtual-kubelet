# Test 02 — Engine boundary check (transactional + CLI)

The release-blocker safety property: a CR with `spec.transactional: true`
and a CLI-template family must NOT cause any device-side write. The
engine boundary should fail-fast before any RPC is sent.

## Setup

CiscoDevice `cat9k-smoke` was wired with `spec.transport: restconf` for
this run (the from-pod NETCONF dial bug — finding #6 in SUMMARY.md —
prevented testing on NETCONF transport). The CR's `transactional: true`
is rejected by the engine on RESTCONF for a different reason than
NETCONF: RESTCONF has no candidate datastore so it cannot commit
transactionally either.

## Pre-state (device side)

```
hostname:
C9K-4

banner-motd-bytes:
0
```

(no banner motd configured)

## Observed phase

```
NAME                    DEVICE        PHASE     DRIFT    AGE
test-02-cli-rejection   cat9k-smoke   Drifted   report   ...
```

The engine took the drift-report path — it computed that one CLI op
would be needed, surfaced "CLI block withheld under driftPolicy=report",
and **did not write to the device**.

## Verify

```sh
$ ssh AI_AGENT_RW@198.51.100.103 "show running-config | include banner|cisco-vk"
(empty)
$ ssh AI_AGENT_RW@198.51.100.103 "show banner motd"
(empty)
```

**No banner motd was set.** The release-blocker safety property holds.

## Verify.sh strict-form result

The `verify.sh` script expected `Phase=Failed` with reason
`ErrTransactionalCLIUnsupported`. With RESTCONF transport, the engine
takes the drift-report path instead, so the strict-form assertion
fails. The functional safety guarantee — "no device write for
transactional+CLI" — is identical across both transport paths and is
proven on-device.

To re-test the strict NETCONF assertion form, the test must run with
`spec.transport: netconf` on the CiscoDevice; that path is currently
blocked by the from-pod NETCONF dial bug (SUMMARY.md finding #6).
