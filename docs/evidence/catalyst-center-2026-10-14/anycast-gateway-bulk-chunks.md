# Bounded anycast requests and original-chunk recovery

The [pinned provider 0.6.1 source](https://raw.githubusercontent.com/CiscoDevNet/terraform-provider-catalystcenter/v0.6.1/internal/provider/resource_catalystcenter_anycast_gateways.go)
splits creates into batches of 20, including creates selected during update.
CVK now adopts that size for new native requests. This is alignment with the
provider, not a claim of an appliance hard limit: the
[official add-anycast API](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/add-anycast-gateways/)
describes an array request without an explicit maximum item count.

## Chunk identity and ordering

Each complete native-fabric cohort remains bounded by the existing 1000-member
inventory limit. Its declarations are sorted by site/pool before static chunks
of 20 are selected. Existing matching members remain in their original source
chunk and are excluded from its native payload. Chunks are not cut from the
remaining missing subset after partial creation.

Large cohorts use a v2 resource identity containing the original source offset,
complete cohort count, chunk count, original selected-member mask, native chunk
dependency/body/incarnation digest and complete canonical source digest. The
same-sized changed tail cannot verify an earlier chunk. Malformed or unaligned
offsets, inconsistent count, noncanonical masks and changed source are refused.
Full cohort allocation checks precede any bounded request, so a conflict beyond
the first 20 members cannot hide behind the first write. Full-state dependency
observations bracket the existing source/ownership guard.

Small cohorts retain v1 resource identities. Acknowledged historical v1 batches
larger than 20 can still verify their complete original footprint; the new native
planner refuses new requests larger than 20. An old unsubmitted oversized journal
is not silently rewritten into a different request. New source chunking does
not mutate claim, lease or retirement protocols.

After a chunk's task/native verification finishes through the existing public
journal, the next reconcile plans the first remaining source chunk. Parent and
zone ordering remains unchanged, and retained bulk recovery remains independent
of the current bulk annotation.

## Verification

Eight native/public cases cover 45 reverse-ordered declarations producing
20/20/5 native requests, restart and no replay, partial first-chunk readback,
late-cohort VLAN conflict, tail pool replacement before dispatch, same-sized
changed source, acknowledged large v1 compatibility and malformed offsets. The
public case traverses real generic journal/section-owner/task contracts with
simulated Kubernetes storage and native HTTPS, reaches InSync, and clears its
journal only after all three chunks. These are fixtures, not installed actors.

Chunk/public race cases passed in 10.757s. Earlier unchanged bulk and parent/IPAM
public cases passed in 20.516s. See
[chunk/public cases](anycast-gateway-bulk-chunks-tests.log) and
[compatibility checks](anycast-gateway-bulk-chunks-compatibility.log).
The complete final race suites passed: Catalyst Center 267.352s and shared
configuration 31.739s; see
[full regression](anycast-gateway-bulk-chunks-full-regression.log). Strict
documentation build and `git diff --check` passed.

The repeated authenticated lab prerequisite check found zero fabric sites and
explicitly skipped accepted execution (0.36s; package 1.012s). See
[native prerequisites](anycast-gateway-bulk-chunks-live-prerequisites.log) and
[sanitized access](anycast-gateway-bulk-chunks-live-access.log). This is a
confirmed setup limitation, not live batch-size or recovery qualification.

## Remaining scope

Accepted live request-size/partial/recovery qualification still needs an isolated
real fabric and its pool/VN prerequisites. Installed workers/API-server, broader
anchored/zone chunk transitions, ownership/adoption/prune and update/delete remain
pending. No full resource or section capability flag was marked complete.
