# LAN START retained-operation validation

This increment adds a private validator for already retained Prepared/Submitting
LAN START operations. It does not enable public START or claim native launch
qualification. NetAsCode remains pinned to 0.5.0.

## Implemented boundary

The adapter uses the existing configuration ownership observation bracket and
shared group identity validator. It checks the current configuration spec,
resolved declaration, source bytes and ConfigMap identity/version where applicable,
section/controller ownership, phase and strict Create resource identity. It
re-observes native preflight evidence and requires exact site/primary-seed IDs
and the complete native ID/serial set for declared seeds and single-device PnP
targets. Changed authentication, source, credential version or ownership fails
closed. Stack discovery is refused at this boundary. The existing START hash
format is preserved; password values are excluded while version commitments remain.

There are no journal transitions, native writes, reservation claims or releases
in this validator. Tests construct retained shared group records; they do not
establish an installed preparation lifecycle or real CiscoDevice/Node admission.
The shared ownership observer now accepts the strict LAN START resource alongside
existing link resources. Existing link metadata policy is factored into a shared
adapter-local predicate without changing its behavior. No shared foundation,
ND, direct-upgrade or SWIM contract changed.

## Validation

- [Focused race fixtures](lan-session-operation-focused.log): PASS, 4.650s.
- [Full affected race suite](lan-session-operation-regression.log): PASS;
  Catalyst Center 507.921s, plus model, configuration, reservations, ND,
  controller and managed protocol packages (cached passes).
- [Manager/worker race checks](lan-session-operation-runtime.log): PASS, 3.761s.

Fixtures cover Prepared/Submitting and ConfigMap sources; missing/pruned or
foreign members; invalid phase/resource/site/seed; late ownership/source/native
authentication changes; changed credential version; cancellation; accepted task
misclassification; forged supplied source bytes; and an in-memory spec change.
They assert no native POST or operation mutation. Production code stayed unchanged
during the full affected suite.

## Remaining work

Actual durable preparation and group dispatch wiring, real managed device/Node
admission, full DHCP/reset-domain containment, authentication eligibility,
allocator capacity/contiguity, projected-secret application receipts,
accepted-task/session recovery, STOP/completion verification, public Report/Apply
and installed/native launch qualification remain open. This validator currently
rejects credential/dependency receipts rather than pretending their lifecycle is
implemented. Explicit discovery lists do not contain the full physical reset
footprint. The previous [read-only lab qualification](lan-session-start.md)
found no eligible PnP/pool fixture and skipped declared launch preflight.

The local CVK Kubernetes API is independently unavailable because of a containerd
sandbox recovery failure. See the [backup and repair investigation](cluster-recovery-backup.md).
