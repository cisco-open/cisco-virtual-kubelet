# Global authentication accepted-result binding

Global authentication updates now persist a versioned resource key in the existing
configuration operation journal. It binds the logical profile name, original
configuration and expected result. The target UUID and accepted task remain in
the existing identity fields; no CRD, transport or provider interface was added.
Fabric authentication uses the same canonical digest helper, preserving its
existing binding format.

The expected result includes native Voice VLAN and BPDU boolean presence/value
that the update preserves. Restarted verification reads the acknowledged profile
twice and checks that result, including when later desired selections are absent.
Native descriptions are excluded from the digest, matching NetAsCode's ignored
field; contract ordering is canonicalized without changing caller-owned slices.
No description, credential or token is stored in the operation key.

The shared journal already retires unsubmitted Prepared operations before
replanning. This change strengthens accepted-operation verification; it does not
introduce a new Prepared replay policy. Verified completion requires a recognized
binding, target UUID, task and submission time with the expected metadata scope.
Malformed or unknown versions retain the operation and report recovery required.
Exact legacy profile-name keys remain recoverable with their previous, narrower
modeled-field and UUID checks; they cannot establish preservation of values that
were never persisted. Unsubmitted Prepared cancellation remains available.

## Verification

Serialized-recovery and public completion fixtures cover Voice VLAN/BPDU drift,
second-read drift, profile replacement, malformed/future keys, missing task or
submission time, ignored descriptions, contract reordering, legacy operations
and Prepared cancellation. The focused authentication/fabric suite passed in
17.050 seconds. Broader configuration, fabric, SWIM and example regression tests
passed in 212.408 seconds. Adjacent test logs record the results.

A native public-reconciler Low Impact change/restore cycle passed using simulated
Kubernetes persistence. Profile UUID was
`0ddb440b-e867-409c-9902-bc7a8c2ceec8`; accepted tasks were
`01a14098-4a4d-7895-96f8-fdf88b421fa4` and
`01a14098-6892-729f-b629-477d54920eab`. Both phases passed correlated task,
current-generation InSync and reconstructed no-op checks. All three complete raw
global profiles matched this run's private baseline after restoration. Test time
was 19.70 seconds (package 21.583 seconds). No fabric or device operation was
submitted. Raw baselines remain outside the repository.

This does not resolve the earlier lost-baseline discrepancy described in
[the original qualification](global-authentication-templates.md). Native fabric
updates remain unqualified after the separate
[empty-fabric application lookup failures](fabric-empty-native-qualification.md).
Installed-cluster persistence/recovery, broader field combinations and reset or
delete ownership remain pending. This is not full resource or NetAsCode parity.
