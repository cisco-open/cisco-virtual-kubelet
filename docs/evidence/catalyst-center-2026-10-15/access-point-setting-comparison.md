# AP selected-setting comparison

## Implemented boundary

The private `observeAccessPointConfigurationCompliance` pipeline freezes desired
intent, invokes existing complete-task/repeated-keyed-readback observation, and
compares selected settings for every member. No source data is modified, no native
write is issued, and no journal, reservation, receipt or lease is advanced.

Each selected payload property is classified using a path and one of:

- Matched: the supported readback value equals the requested value.
- Pending: a supported value is absent or differs.
- Unavailable: its native representation or selection semantics is not qualified.

Diagnostics contain no native values or free-form status text. Gate/control
fields do not create setting ownership. Unselected native properties are ignored;
unknown future selected properties cannot silently disappear. Control fields are
recognized only at their proper top-level or radio scope. Empty selections
cannot supply matching proof.

## Direct comparisons and unresolved representations

Direct comparisons cover hostname, explicit location, brightness, controller
names and controller IP addresses. Desired address objects are compared with
readback strings using parsed IP addresses, including equivalent IPv6 spelling.
Selected scalar radio settings cover antenna pattern/gain, channel number and
power level, with zero values preserved. Radio selection uses the existing native
type mapping; multiple same-type slots remain ambiguous and cannot pass.

The [Cisco keyed readback schema](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/get-access-point-configuration/)
does not define the vocabulary of many response strings or the members of radio
role/band objects. Bool/enum status mappings, role/band objects, cable fields and
site-derived location remain Unavailable. No guessed success strings, truthiness,
first matching radio or task-only success is used to fill those gaps.

These are implementation comparisons, not accepted native AP qualification.
Even matching selected fields do not prove provisioning completion or physical
reservation authority. The public AP execution gate is unchanged.

## Tests

`TestAccessPointSelectedSettingCoverage` exercises all selected top-level and
radio model fields in one real parsed intent. It checks the exact classified
path set, canonical IPv6 comparison, selected zero gain and preservation of all
unsupported representations as unavailable.

`TestAccessPointComparisonDoesNotInventConvergence` covers mismatching/missing
values, missing/wrong/ambiguous radios, future unknown properties, site-derived
location, managed/unmanaged hostname, empty selection and invalid native identity.

The existing full AP observation fixture matrix now runs through the comparison
pipeline, including hostname-only positive comparison, pending or failed tasks,
late child/identity changes, missing/changed readback, authorization failures and
successful tasks with mismatching actual LED status. The latter remains
unavailable until its native representation is qualified.

See `access-point-comparison-affected.log` for the AP/extranet/SWIM race checks.
The final isolated control-scope revision is covered by
`access-point-comparison-final.log`, which reruns complete field accounting,
negative comparisons and the task/readback comparison matrix. These are
affected-test runs, not a new full-suite, installed or native qualification.

## Native and remaining scope

The most recent lab inventory/readback boundary evidence is recorded in
`access-point-observation-native.json`: three network devices, zero APs, and HTTP
404 for a random absent Ethernet-MAC key. No AP mutation is attempted without a
real AP fixture. The native positive comparison cases remain unqualified.

Remaining work includes all unresolved readback representations, provisioning
completion, durable submission/acknowledgement recovery, real physical scope and
actor handoff, atomic verified publication/settlement, public manifest activation
and native AP execution. Overall NetAsCode parity remains incomplete.
