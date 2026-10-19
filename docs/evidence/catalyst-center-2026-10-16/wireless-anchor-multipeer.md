# Anchor-group submission and multi-peer qualification

Date: 2026-10-16. Pinned module: NetAsCode 0.5.0,
`a435555fe828f03cf3311d52fe1ab6572a247916`, `cc_wireless.tf` anchor-group
resource. All eight peer properties retain their existing mapping. This adds
submission checks and qualification; it does not enable whole-resource parity.

## Implementation

The existing anchor executor now reuses the one-use wireless dispatch helper.
Before submission it resolves the original desired peers again and compares their
inventory UUID, hostname, management address, serial, platform and family. Changes
to intent, ambiguous resolution, inventory replacement or a previously external hostname
gaining an inventory match refuse the retained plan. No new client, transport,
worker, task journal or foundation interface was introduced.

The executor then rereads the selected native group, including its identity and
all currently modeled peer options. This last read occurs after inventory
resolution, so a concurrent peer edit during resolution is not overwritten.
Create requires the name to remain absent; Update requires the captured native
observation to remain unchanged. Unrelated groups do not invalidate a plan.
Omitted optional values remain preserved on matching peer identities without
mutating desired intent. Attempts consume the closure even on preflight refusal,
uncertain acknowledgement or concurrent invocation. Durable journal recovery
remains responsible for accepted tasks. These are repeated observations, not an
atomic appliance compare-and-swap; native unknown fields are not attested.

The `examples/configs/catalyst-center/wireless-anchor-multi-peer-apply.yaml` example
shows the complete peer list. Removing a peer is a definition Update; config
Retain policies do not retain omitted members of that list.

## Local verification

`go test -race ./internal/controlleradapter/catalystcenter -run '^TestWirelessAnchor' -count=1`
passes in 3.576s ([log](wireless-anchor-dispatch-tests.log)). Thirteen dispatch
cases include selected-object replacement/removal, newly appeared Create targets,
peer and omitted-option edits, intent mutation, inventory replacement/appearance,
edits during inventory reads, lost acknowledgements and concurrent one-use
submission. They assert exact HTTP mutation counts. The existing public
reconciler test now advances five source generations through Create, priority
change, adding a peer, swapping priorities and removing the original peer. Each
stage simulates a reconciler restart and reaches InSync without replay.

The wireless/configuration regression selection passes in 88.228s
([log](wireless-anchor-regression.log)); the final observation-order change is
covered by the final anchor suite. This is not a claim that all CC tests ran. Adapter registry, complete ND, shared
software lifecycle and software-upgrade packages also pass with race checking
([foundation log](wireless-anchor-foundations.log)).

## Native qualification

`TestLiveWirelessAnchor` with both `CATC_WIRELESS_ANCHOR_EXECUTION` and
`CATC_WIRELESS_ANCHOR_MULTI_PEER` set to `isolated-definition` passes in 14.01s
(package 16.151s; [log](wireless-anchor-multipeer-native.log)). It uses the actual
CVK client, compiled executor, native task polling and independent readback.
The generated unused group `CVK_16063733` retained native ID
`mdeYq1zl-xZe2-mdyZ-nZmZ-qZbwmuSYxZmXndy1mdy2nZm4nZKZ` throughout.

| Stage | Native task ID |
| --- | --- |
| Create | `01a1436e-2310-7c3f-a607-c65a80d4a496` |
| Single-peer priority update | `01a1436e-2b62-7444-b3a3-36a16fe81385` |
| Add second external peer | `01a1436e-32cb-77d0-8428-a6c2b947f10e` |
| Swap priorities | `01a1436e-3a3e-73c1-a1f1-6205235ed634` |
| Remove original peer | `01a1436e-41a7-7537-b162-b30d9209e026` |
| Newer edit before stale attempt | `01a1436e-49d8-7021-8915-c54d11bafc32` |
| Exact captured-ID cleanup | `01a1436e-518c-7921-b97e-687b0fe550b0` |

Each accepted update used the original ID and converged to no-op. After the newer
edit, both the stale attempt and its reuse failed; independent readback proved the
newer state survived. Cleanup restored the complete original empty anchor
inventory. Two earlier native attempts failed on their initial baseline reads,
before any mutation. Read-only Python and Go probes succeeded before the final
run. No TLS policy was weakened beyond the pre-existing explicit lab self-signed
certificate opt-in. There is no retained fixture.

## Remaining limits

Native qualification covers unused external-anchor definitions, not managed WLCs,
associations or mobility traffic. Public reconciliation persistence is simulated;
installed actors were not rolled out. Adoption, per-object durable ownership,
pruning/CR deletion, broader peer combinations and physical deployment remain
pending. Native stale refusal is not a tested automatic retirement path for the
public durable Prepared journal. ND, direct upgrades and SWIM production logic
were not changed. Full 93-resource parity remains incomplete.
