// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

var errLANLinkLegacyRecovery = errors.New("legacy LAN ADD record lacks an immutable recovery binding")
var errLANLinkSubmissionUncertain = errors.New("LAN ADD submission outcome has no acknowledged task")

// Exclude expected ADD outputs (port/address/routing state, discovered allocated
// address lists and usage counters), while binding immutable device/port and
// original discovery/pool identities. Full current observations still bracket
// proof; excluded fields cannot stand in for independently verified results.
func lanLinkAddRecoveryDigest(deps *lanLinkDependencies) (string, error) {
	if deps == nil || deps.adjacency == nil || deps.adjacency.endpoints == nil || deps.pool == nil {
		return "", errConfigurationTransition
	}
	type portIdentity struct{ ID, Device, Name, Type, Index, Mapped string }
	type endpointIdentity struct {
		ID, Serial, Address, Hostname, Family, Platform, Software string
		Port                                                      portIdentity
	}
	endpoint := func(e lanLinkEndpoint) endpointIdentity {
		return endpointIdentity{e.device.ID, e.device.Serial, e.device.ManagementIP, e.device.Hostname, e.device.Family, e.device.PlatformID, e.device.SoftwareVersion, portIdentity{e.port.ID, e.port.DeviceID, e.port.PortName, e.port.Type, e.port.Index, e.port.MappedID}}
	}
	type originIdentity struct{ Session, Site, Created, Name, Serial string }
	origins := func(scope lanLinkDiscoveryScope) []originIdentity {
		out := []originIdentity{}
		for _, origin := range scope.origins {
			out = append(out, originIdentity{origin.SessionID, origin.Site, origin.Created, origin.Device.Name, origin.Device.Serial})
		}
		return out
	}
	raw, err := json.Marshal(struct {
		Primary, Secondary               endpointIdentity
		PrimarySite, SecondarySite       Site
		PrimaryOrigins, SecondaryOrigins []originIdentity
		Pool                             string
	}{endpoint(deps.adjacency.endpoints.primary), endpoint(deps.adjacency.endpoints.secondary), deps.pool.primary.site, deps.pool.secondary.site, origins(deps.pool.primary), origins(deps.pool.secondary), deps.pool.pool.digest})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func validateLANLinkAddCompletionOperation(op *cvk.NetworkControllerConfigurationOperation, now time.Time) (lanLinkAddBinding, error) {
	var zero lanLinkAddBinding
	if op == nil || op.Section != "lan_automation" || op.TargetID == op.ParentID || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.TargetSerial != "" || op.CreatedResourceID != "" || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || op.CancellationRequestedAt != nil || (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || contract.ValidateGroupJournalIdentity(op) != nil || len(op.PhysicalGroup.Targets) != 2 {
		return zero, errConfigurationTransition
	}
	binding, err := parseLANLinkAddResource(op.Resource, op.Action)
	if err != nil {
		return zero, err
	}
	if binding.version != 2 {
		return zero, errLANLinkLegacyRecovery
	}
	return binding, nil
}

// Select the original source member by native endpoint/port identity and logical
// digest. Another not-yet-executed link's missing port is not a completion gate.
func (a *adapter) retainedLANLinkAdd(ctx context.Context, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (lanLinkIntent, error) {
	var result lanLinkIntent
	binding, err := validateLANLinkAddCompletionOperation(op, now)
	if err != nil {
		return result, err
	}
	wants, err := declaredLANLinks(source)
	if err != nil {
		return result, err
	}
	inventory, err := a.client.configurationDevices(ctx)
	if err != nil {
		return result, err
	}
	physical, err := bindMulticastRPDevices([]string{op.TargetID, op.ParentID}, inventory)
	if err != nil {
		return result, err
	}
	devices := map[string]Device{}
	for _, device := range physical {
		devices[device.ID] = device
	}
	for _, target := range op.PhysicalGroup.Targets {
		if devices[target.InventoryID].Serial != target.PhysicalIdentity {
			return result, errConfigurationStale
		}
	}
	primary, secondary := devices[op.TargetID], devices[op.ParentID]
	pports, err := a.client.lanLinkInterfaces(ctx, primary.ID)
	if err != nil {
		return result, err
	}
	sports, err := a.client.lanLinkInterfaces(ctx, secondary.ID)
	if err != nil {
		return result, err
	}
	found := false
	for _, want := range wants {
		if want.action != "ADD_LINK" {
			continue
		}
		if !sameManagementIP(want.primaryIP, primary.ManagementIP) || !sameManagementIP(want.secondaryIP, secondary.ManagementIP) {
			continue
		}
		p, err := bindLANLinkInterface(primary.ID, want.primaryInterface, pports)
		if err != nil {
			continue
		}
		s, err := bindLANLinkInterface(secondary.ID, want.secondaryInterface, sports)
		if err != nil {
			continue
		}
		logical, err := lanLinkAddLogicalDigest(want, &lanLinkEndpointSnapshot{primary: lanLinkEndpoint{device: primary, port: p}, secondary: lanLinkEndpoint{device: secondary, port: s}})
		if err != nil {
			return result, err
		}
		if logical != binding.logical {
			continue
		}
		if found {
			return result, errConfigurationStale
		}
		found = true
		result = want
	}
	if !found {
		return result, errConfigurationStale
	}
	return result, nil
}

type lanLinkCompletionSnapshot struct {
	dependencies *lanLinkDependencies
	sessions     *lanActiveSessionSnapshot
	usage        *lanPoolUsage
}

// Completion observes post-ADD state without imposing the pre-ADD empty-port or
// minimum-free-space checks on an operation which already consumed addresses.
func (c *client) lanLinkCompletionTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want lanLinkIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*lanLinkCompletionSnapshot, []contract.GroupTargetIdentity, error) {
	binding, err := validateLANLinkAddCompletionOperation(op, now)
	if err != nil {
		return nil, nil, err
	}
	snapshot, targets, err := observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*lanLinkCompletionSnapshot], error) {
		sessions, err := c.lanActiveSessions(ctx)
		if err != nil {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, err
		}
		if len(sessions.Active) != 0 {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, errConfigurationBusy
		}
		deps, err := c.lanLinkDependencies(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, err
		}
		if deps.adjacency.link == nil || deps.pool == nil {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, errConfigurationBusy
		}
		endpoints := deps.adjacency.endpoints
		logical, err := lanLinkAddLogicalDigest(want, endpoints)
		if err != nil {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, err
		}
		recovery, err := lanLinkAddRecoveryDigest(deps)
		if err != nil {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, err
		}
		if logical != binding.logical || recovery != binding.recovery || endpoints.primary.device.ID != op.TargetID || endpoints.secondary.device.ID != op.ParentID {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, errConfigurationStale
		}
		usage, err := c.lanPoolUsage(ctx, deps.pool.pool.primary)
		if err != nil {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, err
		}
		physical, err := bindMulticastRPDevices([]string{op.TargetID, op.ParentID}, []Device{endpoints.primary.device, endpoints.secondary.device})
		if err != nil {
			return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{}, err
		}
		state := &lanLinkCompletionSnapshot{deps, sessions, usage}
		raw, _ := json.Marshal(struct {
			Dependencies string
			Sessions     *lanActiveSessionSnapshot
			Usage        *lanPoolUsage
		}{deps.observationDigest(), sessions, usage})
		return configurationPhysicalObservation[*lanLinkCompletionSnapshot]{state: state, physical: physical, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
		return nil, nil, errConfigurationStale
	}
	return snapshot, targets, nil
}

func (a *adapter) beginLANLinkAddVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || !supportedLANLinkAddConfig(config) || authorize == nil {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	want, err := a.retainedLANLinkAdd(ctx, source, op, now)
	if err != nil {
		return err
	}
	return a.beginLANLinkVerification(ctx, config, source, nc, op, members, authorize, now, func(ctx context.Context) error {
		_, _, err := a.client.lanLinkCompletionTargets(ctx, a.statusReader, config.Namespace, want, op, now)
		return err
	})
}

// Shared transition only; this cannot publish completion or release members.
func (a *adapter) beginLANLinkVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time, observe func(context.Context) error) error {
	return contract.BeginGroupVerification(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		done, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "lan_automation", func(ctx context.Context) (bool, error) {
			completed, err := a.client.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
			if err != nil {
				return false, err
			}
			if completed.IsZero() || completed.After(now) {
				return false, errConfigurationBusy
			}
			err = observe(ctx)
			return err == nil, err
		})
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		return nil
	}, now)
}
