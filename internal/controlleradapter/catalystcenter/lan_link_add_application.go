// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net"
	"net/netip"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

// Positive native IPv4 result checks precede independent device proof. The
// allocator chooses addresses; both usable interface addresses must fit one
// subnet inside the original LAN pool. This is not an ISIS convergence proof.
func lanLinkAddNativeResult(snapshot *lanLinkCompletionSnapshot) error {
	if snapshot == nil || snapshot.dependencies == nil || snapshot.dependencies.adjacency == nil || snapshot.dependencies.adjacency.link == nil || snapshot.dependencies.adjacency.endpoints == nil {
		return errConfigurationBusy
	}
	adjacency := snapshot.dependencies.adjacency
	if !strings.EqualFold(adjacency.link.Status, "up") {
		return errConfigurationBusy
	}
	for _, endpoint := range []lanLinkEndpoint{adjacency.endpoints.primary, adjacency.endpoints.secondary} {
		if !strings.EqualFold(endpoint.port.AdminStatus, "up") || !strings.EqualFold(endpoint.port.Status, "up") {
			return errConfigurationBusy
		}
	}
	return lanLinkConfiguredIPv4(snapshot.dependencies)
}

// Configuration ownership can remain valid on a down link. ADD completion still
// requires positive liveness above; DELETE cannot depend on working forwarding.
func lanLinkConfiguredIPv4(deps *lanLinkDependencies) error {
	if deps == nil || deps.pool == nil || deps.adjacency == nil || deps.adjacency.endpoints == nil {
		return errConfigurationBusy
	}
	endpoints := deps.adjacency.endpoints
	space := deps.pool.pool.primary.V4
	if space == nil || space.PrefixLength == nil {
		return errConfigurationStale
	}
	poolAddress, err := netip.ParseAddr(space.Subnet)
	if err != nil || !poolAddress.Is4() {
		return errInvalidResponse
	}
	pool := netip.PrefixFrom(poolAddress, *space.PrefixLength)
	if !pool.IsValid() || pool.Masked() != pool {
		return errInvalidResponse
	}
	addresses := []netip.Addr{}
	prefixes := []netip.Prefix{}
	for _, endpoint := range []lanLinkEndpoint{endpoints.primary, endpoints.secondary} {
		address, err := netip.ParseAddr(endpoint.port.IPv4)
		if err != nil || !address.Is4() || !address.IsGlobalUnicast() {
			return errConfigurationBusy
		}
		mask, err := netip.ParseAddr(endpoint.port.Mask)
		if err != nil || !mask.Is4() {
			return errConfigurationBusy
		}
		raw := mask.As4()
		bits, total := net.IPMask(raw[:]).Size()
		if total != 32 || bits < pool.Bits() || bits >= 32 {
			return errConfigurationBusy
		}
		prefix := netip.PrefixFrom(address, bits).Masked()
		if !pool.Contains(address) || !pool.Contains(prefix.Addr()) {
			return errConfigurationStale
		}
		for _, other := range []lanLinkEndpoint{endpoints.primary, endpoints.secondary} {
			if sameManagementIP(address.String(), other.device.ManagementIP) {
				return errConfigurationStale
			}
		}
		if bits < 31 {
			start := prefix.Addr().As4()
			ip := address.As4()
			broadcast := true
			for i := range ip {
				if ip[i] != (start[i] | ^raw[i]) {
					broadcast = false
				}
			}
			if address == prefix.Addr() || broadcast {
				return errConfigurationBusy
			}
		}
		addresses = append(addresses, address)
		prefixes = append(prefixes, prefix)
	}
	if addresses[0] == addresses[1] || prefixes[0] != prefixes[1] {
		return errConfigurationBusy
	}
	link := deps.adjacency.link
	if link != nil && (link.StartIP != "" && link.StartIP != endpoints.primary.port.IPv4 || link.EndIP != "" && link.EndIP != endpoints.secondary.port.IPv4 || link.StartMask != "" && link.StartMask != endpoints.primary.port.Mask || link.EndMask != "" && link.EndMask != endpoints.secondary.port.Mask) {
		return errConfigurationStale
	}
	return nil
}

func (c *client) verifiedLANLinkAddReceipt(ctx context.Context, reader ctrlclient.Reader, namespace string, want lanLinkIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time, proof configurationGroupConvergenceProof) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	binding, err := validateLANLinkAddCompletionOperation(op, now)
	if err != nil {
		return nil, err
	}
	if op.Phase != "Verifying" || proof == nil {
		return nil, errConfigurationDeviceAdmission
	}
	collect := func(ctx context.Context) (*lanLinkCompletionSnapshot, []contract.GroupTargetIdentity, time.Time, error) {
		completed, err := c.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		if completed.IsZero() || completed.After(now.UTC().Truncate(time.Second)) {
			return nil, nil, time.Time{}, errConfigurationBusy
		}
		native, targets, err := c.lanLinkCompletionTargets(ctx, reader, namespace, want, op, now)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		if err := lanLinkAddNativeResult(native); err != nil {
			return nil, nil, time.Time{}, err
		}
		return native, targets, completed, nil
	}
	native, err := observeConfigurationGroupConvergence(ctx, op, now, collect, proof)
	if err != nil {
		return nil, err
	}
	result, err := lanLinkAddResultDigest(native)
	if err != nil {
		return nil, err
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "lan_automation", Resource: "lan-link:" + binding.logical + ":" + result, Operation: *verified, VerifiedAt: &at}, nil
}

func (a *adapter) recordLANLinkAddApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, proof configurationGroupConvergenceProof, now time.Time) error {
	return a.recordLANLinkApplication(ctx, config, operationID, members, authorize, proof, now, func(ctx context.Context, source *configSourceSnapshot, nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation, guarded configurationGroupConvergenceProof) (*cvk.NetworkControllerConfigurationActionApplication, error) {
		want, err := a.retainedLANLinkAdd(ctx, source, op, now)
		if err != nil {
			return nil, err
		}
		return a.client.verifiedLANLinkAddReceipt(ctx, a.statusReader, config.Namespace, want, op, now, guarded)
	})
}

func validateLANLinkAddVerifiedReceipt(receipt *cvk.NetworkControllerConfigurationActionApplication, now time.Time) (lanLinkAddBinding, error) {
	var zero lanLinkAddBinding
	if receipt == nil || receipt.Section != "lan_automation" || receipt.Operation.Phase != "Verified" || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.Operation.SubmittedAt == nil || receipt.VerifiedAt.Before(receipt.Operation.SubmittedAt) || receipt.VerifiedAt.After(now) {
		return zero, errConfigurationStale
	}
	op := receipt.Operation.DeepCopy()
	op.Phase = "Verifying"
	return validateLANLinkAddCompletionOperation(op, now)
}
