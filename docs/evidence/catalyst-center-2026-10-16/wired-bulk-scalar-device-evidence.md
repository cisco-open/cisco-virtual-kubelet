# Wired bulk: scoped banner and fixed-UTC device evidence

The existing scoped network comparison now also supports complete Custom MOTD
blocks and four fixed-UTC timezone identifiers: `UTC`, `Etc/UTC`, `GMT` and
`Etc/GMT`. This extends the existing NTP/DNS stage. It does not enable public
bulk execution or provide a complete provisioning convergence proof.

## Semantics and boundaries

Banner observations use `show running-config | section ^banner motd`. The parser
requires the MOTD command, an opening/closing delimiter and no trailing CLI or
second block. Supported delimiters are displayed `^C`, raw ETX and a single
printable ASCII character other than caret. Body whitespace and newlines are
compared exactly against the original declared source. Transport CRLF is
canonicalized to LF; body text is not trimmed or expanded. Builtin banners,
incomplete output, unrelated login-banner blocks, malformed UTF-8 and unsupported
display forms are refused. This bounded MOTD profile still needs native
qualification of the Catalyst Center rendering and worker output.

Clock observations use `show running-config all | include ^clock`, allowing a
platform to expose default values without inferring a default from silence.
Exactly one observed `clock timezone UTC|GMT 0 [0]` is required. Missing timezone
output, different labels/offsets, duplicate settings, summer-time rules and
unrecognized clock configuration are refused. Arbitrary regional IANA zones
remain unavailable: matching their current offset is not proof of the correct
future daylight-saving behavior. No device clock or timezone is changed.

Query text is fixed; banner messages, addresses and other model values are not
inserted into commands. Every generated command passes the existing IOS-XE
diagnostic command validator. The production adapter adds no dependency on a
device transport or new credential reader. Status freshness, manager/worker
identity, output redaction/truncation, source revisions and complete native group
bindings remain enforced by the existing worker collector.

Cisco's [IOS-XE clock guide](https://www.cisco.com/c/en/us/td/docs/routers/ios/config/17-x/syst-mgmt/b-system-management/m_bsm-time-calendar-set.html)
documents timezone offsets and separate summer-time configuration. The
[Catalyst 9300 system management guide](https://www.cisco.com/c/en/us/td/docs/switches/lan/catalyst9300/software/release/17-12/configuration_guide/sys_mgmt/b_1712_sys_mgmt_9300_cg.pdf)
documents delimited MOTD configuration. The
[IOS-XE show command reference](https://www.cisco.com/c/en/us/td/docs/switches/lan/catalyst9300/software/release/17-15/command_reference/b_1715_9300_cr/system_management_commands.html)
describes default-expanded running configuration. These references establish
command grammar; they do not constitute native qualification of this integration.

## Verification

- [Final safety log](wired-bulk-scalars-safety.log): 20 banner cases, 17 clock
  cases, existing NTP/DNS comparisons, command-policy compatibility and nine
  source/native/task/worker compositions. The mixed matching composition passes;
  mismatched/incomplete/redacted banner output, wrong timezone and configured
  summer time are refused. Manager and device-worker results are simulated.
  Matching scoped evidence leaves the operation Verifying with no full-device
  receipt or reservation completion.
- [Private API-server log](wired-bulk-scalars-api.log): ordinary and multiline
  banner/fixed-UTC results round-trip through real DeviceOperation CRDs, object
  identities, timestamps, generations and status writes. Scalar meaning is
  preserved. Redaction and changed query specifications remain refused. Actual
  actor writes are simulated, not installed admission/RBAC qualification.
- [Regression log](wired-bulk-scalars-regression.log): bulk, shared configuration,
  network-setting, site/floor/AP suites pass with the race detector.
- [Foundation log](wired-bulk-scalars-foundations.log): registry, complete ND
  adapter, reservation/configuration contracts, lifecycle/upgrade, DeviceOperation
  and diagnostic policy/redaction package suites pass.
- [Native read-only log](wired-bulk-scalars-native.log): all three lab switch
  identities were read; the original timezone policy matched, a changed desired
  source was refused and original policy was rechecked. Source-derived worker
  query plans were supported for all three, with zero unsupported plans. Test
  time 40.07 seconds (package 42.444 seconds). Configuration writes zero under
  the transport guard. No worker result was read and no native banner/clock
  device verification is claimed. The final clock query's default expansion is
  fixture/command-policy tested; it has not been executed on the lab devices.

## Remaining work

Native banner and clock output qualification, default/variant display forms,
other timezone rules, DHCP/telemetry/AAA/credential device semantics, switching
profiles/templates, full rendered-intent retention, replaced-source historical
recovery, installed workers and accepted provisioning cycles remain pending.
This remains a scoped comparison, not a final `wiredBulkConvergenceProof`.
Public bulk execution and all whole-resource/section parity flags stay gated.
