# Catalyst Center discovery defaults

`controller-export-discovery --defaults FILE` resolves supported non-secret
`defaults.catalyst_center.inventory.discovery` fields before the existing site
selector and execution compiler. The pinned 0.5.0 bundle defines no discovery
defaults. Missing required values therefore need explicit job values or supported
overrides; provider behavior is not guessed.

The resolution order is pinned defaults, explicit override defaults, then each
job's own fields. Only absent fields receive defaults. Explicit nulls are not
repaired: supported optional nulls remain unselected and suppress a non-null
fallback, while unresolved required values and incompatible jobs are refused. Every supplied default,
including unused or shadowed values, validates through the existing discovery
parser before selection. Invalid excluded jobs also remain errors.

Supported fields are `type`, `protocol_order`, `ip_address_list`, `ip_filter_list`,
`cdp_level`, `lldp_level`, `retry`, `time_out`, `netconf_port` and
`preferred_mgmt_ip_method`. Values use the existing resolved execution types;
for example, `netconf_port` is a canonical string. Job names, credential-name
lists and `create_per_site` remain explicit declarations. Inline passwords,
communities and other credential defaults are unsupported.

Optional null is supported for `cdp_level`, `lldp_level`, `retry`, `time_out`,
`netconf_port`, `preferred_mgmt_ip_method` and `ip_filter_list`. Those keys may
also be null in an override file. They send no native property; zero and empty
lists retain their distinct declared-value behavior. Null name, type, address,
protocol, credential references and unsupported fields remain errors.

The checked-in examples are model data, not Kubernetes manifests:

- `examples/configs/catalyst-center/discovery-defaults-input.yaml`
- `examples/configs/catalyst-center/discovery-defaults.yaml`

From the repository root, choose a new output path:

```sh
go run ./tools/cvk-netascode-migrate controller-export-discovery \
  --model-version 0.5.0 \
  --defaults examples/configs/catalyst-center/discovery-defaults.yaml \
  --output ./discovery-resolved.json \
  examples/configs/catalyst-center/discovery-defaults-input.yaml
```

The output contains the resolved `catalyst_center` model and is created mode 0600
without overwriting an existing file. Standard output contains provenance and
counts, not model values. The report records input/defaults/override/output,
policy and pinned Terraform digests. `appliedDefaultFields` counts expansion
across all declared jobs before selection. Existing managed-site flags retain
their semantics, and omitting `--defaults` retains the existing command behavior.

Place the resolved model in the ordinary config source, such as a ConfigMap
referenced by `NetworkControllerConfig.spec.source.configMapRef`, using the same
inventory owner as earlier revisions. Set the usual pinned model version and
`resolved: true`; the reported exporter identifies the conversion policy.
Registered global credential names must already resolve on that controller.
Changing defaults requires exporting a fresh resolved source revision. The
runtime does not merge default files or introduce another credential resolver,
transport or journal writer.

The exporter enforces the existing 100-job and 1-MiB resolved-model bounds. It
budgets repeated default expansion before serialization, then applies the usual
selection/output limits. It does not implement file merging, whole-model or full
nested-schema/default parity. Null clearing and inline credential projection need
separate execution contracts. Optional null does not reset native state; see
[null/omission qualification](../evidence/catalyst-center-2026-10-18/discovery-optional-null.md).

The exact upstream expressions are retained from
[pinned cc_discovery.tf](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_discovery.tf).
Controlled and real-API tests exercise resolved ownership/replacement/recovery.
Native lab qualification covers type/protocol defaults, protocol replacement,
three retained-task recovery faults and exact cleanup. Kubernetes persistence and
Unknown-phase fault injection are simulated in that native run; no installed
actor restart is claimed. Broader field combinations, installed qualification
and full parity remain open. See
[qualification evidence](../evidence/catalyst-center-2026-10-18/discovery-defaults.md).
