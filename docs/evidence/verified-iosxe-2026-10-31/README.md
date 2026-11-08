# Verified IOS-XE qualification — access preflight

Status: **BLOCKED; no physical qualification cases executed.**

The configured historical IOS-XE control plane, Ubuntu16, rejected SSH
authentication. Ubuntu17 was reachable, but its current cluster had no IOS-XE
node. Ubuntu12 was reachable, but listed only its control-plane node. A retry
from Ubuntu17 with the workstation's existing host-key pin verified the target
host and also failed authentication. Strict host-key checking was preserved.

No device configuration, application package, worker deployment, journal or
cluster workload was changed during this preflight. No image update, rollback,
checksum injection or worker restart has been claimed as executed.

- [preflight.json](preflight.json) records the attempted routes and results.
- [qualification.json](qualification.json) binds the current source worktree
  and explicitly marks every requested physical case `NOT_RUN`. Its base Git
  commit is not represented as the uncommitted implementation's build identity.
- [Qualification procedure](../../verified-iosxe-qualification.md) specifies the
  target binding, release inputs, serial test matrix and required observations.

The user was asked for a working lab SSH alias or kubeconfig and an available
IOS-XE CiscoDevice target. Once access is restored, obtain a fresh baseline and
retain the candidate's deployed image/binary identities before running the
matrix. Preserve this failed preflight when recording the subsequent attempt.

Standard remains the default. Governed is deferred. This report contains no
device credentials, producer private keys or kubeconfig data.
