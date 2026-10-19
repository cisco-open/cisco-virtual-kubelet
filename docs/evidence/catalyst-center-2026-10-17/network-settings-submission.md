# Network settings: frozen submission and no-dispatch recovery

NTP, DHCP, DNS, timezone, banner and telemetry Updates now freeze their complete
selected settings cohort and re-observe it twice before the one-use native
request. Previously their closures retained only a payload and endpoint; they
could submit after native state changed and could be invoked again. This change
closes that executable lifecycle gap without adding a second journal, transport,
ownership mechanism or foundation/provider API.

## Source and native boundaries

The [pinned 0.5.0 network settings resources](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_network_settings.tf)
map explicit selected settings to site UUIDs. CVK now freezes the resolved binding
and payload, including nested telemetry maps and server lists. Empty lists remain
explicit empty lists. Caller edits cannot retarget a prepared closure.

The cohort observation records exact selected site/ancestor incarnations,
direct settings and effective inherited settings. Native site/kind reads are
cached only within one observation, using the existing inheritance resolver.
Resolving telemetry inheritance operates on detached copies; it cannot overwrite
the saved direct-state baseline. Same-value transitions between inheritance and
explicit overrides still invalidate the original plan. Valid unrelated branches
and unselected settings do not broaden the mutation footprint. Unsupported
payloads refuse before native observation. This cohort covers these six settings;
it does not claim fresh whole-source protection for every credential/IPAM or
other resource combined with them.

The existing `guardConfigurationPreflight` supplies one-use dispatch and an
original-guard refusal capability. The existing no-dispatch protocol now accepts
only the six recognized site-setting Update identities with a native target
UUID and the existing metadata-only restrictions. A proven preflight failure can
retire its unchanged taskless journal while preserving ownership/history, then
replan from current state. A mutation failure or malformed/lost acknowledgement
cannot acquire this proof: its Unknown operation remains fenced without replay.
This does not enable acknowledged Unknown-task recovery for these resources.

## Verification

- [Race regression](network-settings-submission-regression.log), 16.625 seconds:
  72 new submission cases across all six setting types cover selected/local and
  companion drift, inherited values, explicit/inherited provenance, ancestor
  replacement, second-observation changes, read errors, unrelated changes,
  caller mutation, lost acknowledgements and concurrent/repeated use. Twenty-four
  public fixture cases cover successful completion, preflight read refusal,
  external convergence and ambiguous write retention. Existing native comparison,
  telemetry and shared no-dispatch tests pass, including 239 shared refusal
  subcases after adding these six identities.
- [Actual Kubernetes API matrix](network-settings-submission-api.log), 16.621
  seconds: all 24 public lifecycle cases pass with real configuration/controller
  UID, generation, resourceVersion and status persistence. Native responses and
  actor behavior are controlled fixtures; no installed admission is claimed.
- [Real lab public reconciliation](network-settings-submission-native.log),
  84.082 seconds: generated hierarchy
  `Global/CVK-Q-20261017-024006-000000000`; one synthetic read-only preflight
  failure for each setting; six original no-dispatch refusals retired and
  replanned; exactly six accepted setting tasks/writes reached InSync. Repeated
  no-op reconciliation sent no additional mutation. NTP/DHCP/DNS/timezone/banner
  and built-in telemetry readback all converged. Three generated hierarchy
  Creates and one floor Update were setup actions; exact hierarchy cleanup was
  verified. Kubernetes persistence/restarts in this native run are simulated.
  No existing sites, device provisioning, SWIM or device configuration were
  targeted; external telemetry collectors and client/device behavior were not
  qualified.
- [Adjacent credential/bulk inheritance checks](network-settings-submission-adjacent.log)
  and [shared foundation tests](network-settings-submission-foundations.log)
  cover existing composition, ND, device workers, diagnostics, lifecycle,
  upgrades and GNOI. Unchanged foundation packages may use cached results.
- [Current source/pinned module fingerprints](network-settings-submission-sources.json)
  and [strict documentation build](network-settings-submission-docs.log).

## Remaining scope

Retain behavior is unchanged. Acknowledged Unknown-task recovery, complete
latest-applied ownership/baseline semantics, destructive reset/prune transitions,
broader defaults/inheritance and telemetry collector combinations, installed
rollout and actual rendered-device behavior still need qualification or
implementation. SDA semantic verification remains a separate open dependency;
these setting tests do not close that gate. Full pinned-model parity is incomplete.
