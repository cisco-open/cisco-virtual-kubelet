// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

var errFabricEventConvergenceUnavailable = errors.New("pending fabric-event device convergence verifier is unavailable")
var errSDATransitConvergenceUnavailable = errors.New("SDA transit device convergence verifier is unavailable")

// Retained group actions precede new-intent compilation. A Prepared fabric action
// may dispatch only under explicit group installation and the all-member
// one-use permit; acknowledged actions only observe and record verification.
func (r *configReconciler) recoverAcknowledgedFabricGroup(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, error) {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return false, nil
	}
	op := nc.Status.Configuration.Operation
	extranet := isExtranetGroupResource(op.Section, op.Resource)
	if extranet && !r.a.configurationGroupAdmissionReady() {
		return false, nil
	}
	fabricBulk := op.Section == "fabric" && strings.HasPrefix(op.Resource, fabricDevicesBulkFamily)
	bulk := op.Section == "inventory" && strings.HasPrefix(op.Resource, wiredBulkProvisionPrefix)
	sdaTransit := op.Section == "fabric" && strings.HasPrefix(op.Resource, sdaTransitCreateFamily)
	if sdaTransit && (op.PhysicalGroup == nil || !validConfigurationGroupAction(op.Section, op.Resource, op.Action)) {
		// Preserve the existing explicit reserved/pruned-group recovery path.
		return false, nil
	}
	if (bulk || fabricBulk || sdaTransit) && op.Phase == "Prepared" {
		return false, nil
	}
	ewlc := op.Section == "inventory" && strings.HasPrefix(op.Resource, "fabric-ewlc-update:")
	fabricAuth := op.Section == "fabric" && strings.HasPrefix(op.Resource, fabricAuthManagedPrefix)
	if !(sdaTransit || extranet || bulk || fabricBulk || ewlc || fabricAuth || op.Section == "fabric" && (strings.HasPrefix(op.Resource, "fabric-replication-update:") || strings.HasPrefix(op.Resource, "fabric-multicast-vn:") || strings.HasPrefix(op.Resource, "fabric-sda-handoff-create:") || strings.HasPrefix(op.Resource, "fabric-l3-ip-handoff-create:") || strings.HasPrefix(op.Resource, "fabric-l2-handoff-create:") || strings.HasPrefix(op.Resource, "fabric-port-channel-create:") || strings.HasPrefix(op.Resource, "fabric-port-assignment-create:") || strings.HasPrefix(op.Resource, "fabric-wireless-multicast-update:") || strings.HasPrefix(op.Resource, "fabric-vlan-ssid-update:") || isPendingFabricEventOperation(op.Section, op.Resource))) || (op.Phase != "Prepared" && op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") {
		return false, nil
	}
	if op.Phase == "Prepared" && (!r.a.configurationGroupAdmissionReady() || isPendingFabricEventOperation(op.Section, op.Resource)) {
		return false, nil
	}
	if !r.a.configurationAdmissionReady() || source == nil || op.PhysicalGroup == nil || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return true, errConfigurationStale
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var members []*ops.ControllerConfigurationReservation
	if op.Phase != "Prepared" {
		var err error
		members, err = contract.ReadAcknowledgedGroupReservations(recoveryCtx, r.a.statusReader, nc, time.Now())
		if err != nil {
			return true, err
		}
	}
	authorize := func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		for _, member := range current {
			if err := r.a.authorizeConfigurationGroupMember(ctx, member); err != nil {
				return err
			}
		}
		return nil
	}
	if err := authorize(recoveryCtx, members); err != nil {
		return true, err
	}
	if sdaTransit {
		return true, r.recoverSDATransitGroup(recoveryCtx, config, source, op, members, authorize)
	}
	if extranet {
		return true, r.recoverExtranetGroup(recoveryCtx, config, source, op, members, authorize)
	}
	if fabricAuth {
		return true, r.recoverFabricAuthenticationGroup(recoveryCtx, config, source, op, members, authorize)
	}
	if fabricBulk {
		if op.Phase == "Verifying" {
			if !r.a.configurationGroupAdmissionReady() {
				return true, errFabricDevicesConvergenceUnavailable
			}
			queries := make(map[string][]string, len(op.PhysicalGroup.Targets))
			for _, target := range op.PhysicalGroup.Targets {
				queries[target.InventoryID] = fabricDeviceEvidenceCommands()
			}
			if _, err := r.a.collectFabricDevicesBulkEvidence(recoveryCtx, config, source, op, members, authorize, queries, time.Now()); err != nil {
				return true, err
			}
			return true, errFabricDevicesConvergenceUnavailable
		}
		return true, r.a.beginFabricDevicesBulkVerification(recoveryCtx, config, source, members, authorize, time.Now())
	}
	if bulk {
		if op.Phase == "Verifying" {
			return true, r.a.recoverWiredBulkNetworkEvidence(recoveryCtx, config, source, op, members, authorize, time.Now())
		}
		return true, r.a.beginWiredBulkVerification(recoveryCtx, config, source, members, authorize, time.Now())
	}
	if op.Section == "fabric" && strings.HasPrefix(op.Resource, "fabric-replication-update:") {
		return true, r.recoverFabricReplicationGroup(recoveryCtx, config, source, op, members, authorize)
	}
	if op.Section == "fabric" && strings.HasPrefix(op.Resource, "fabric-multicast-vn:") {
		return true, r.recoverFabricMulticastVNGroup(recoveryCtx, config, source, op, members, authorize)
	}
	if ewlc {
		intents, err := parseFabricEWLCIntents(source.raw)
		if err != nil {
			return true, err
		}
		inventory, err := r.a.client.configurationDevices(recoveryCtx)
		if err != nil {
			return true, err
		}
		selected := ""
		for _, want := range intents {
			device, err := resolveConfigurationDevice(want.device.inventory.reference, inventory)
			if err != nil {
				return true, err
			}
			if device.ID == op.TargetID {
				if op.Phase == "Prepared" && !want.settings.enabled {
					return true, errFabricEWLCDisableUnavailable
				}
				if selected != "" {
					return true, errConfigurationStale
				}
				selected = want.device.inventory.reference.Name
			}
		}
		if selected == "" {
			return true, errConfigurationStale
		}
		switch op.Phase {
		case "Prepared":
			if err := r.a.revalidateFabricEWLCOperation(recoveryCtx, config, source, selected, op, "Prepared"); err != nil {
				return true, err
			}
			permit, err := r.a.claimConfigurationGroup(recoveryCtx)
			if err != nil {
				return true, err
			}
			_, err = r.a.dispatchFabricEWLC(recoveryCtx, config, source, selected, permit)
			return true, err
		case "Verifying":
			return true, r.a.recordFabricEWLCApplication(recoveryCtx, config, op.ID, selected, members, authorize, time.Now())
		default:
			return true, r.a.beginFabricEWLCVerification(recoveryCtx, config, source, selected, members, authorize, time.Now())
		}
	}
	if strings.HasPrefix(op.Resource, "fabric-vlan-ssid-update:") {
		plan, err := r.a.client.compileFabricConfiguration(source.raw)
		if err != nil {
			return true, err
		}
		multicast, ok := plan.configurationExecutor.(*fabricVLANSSIDConfiguration)
		if !ok {
			return true, errConfigurationTransition
		}
		selected := ""
		expected, _ := parseFabricVLANSSIDResource(op.Resource)
		for _, site := range multicast.desired {
			state, err := r.a.client.observeFabricVLANSSIDs(recoveryCtx, site.path)
			if err != nil {
				return true, err
			}
			identity, err := fabricVLANSSIDResource(site.path, &fabricVLANSSIDDependencies{siteID: state.siteID, fabricID: state.fabricID, before: state.mappings, payload: state.mappings})
			if err != nil {
				return true, err
			}
			if state.siteID != op.ParentID || state.fabricID != op.TargetID || strings.Split(identity, ":")[1] != expected[1] {
				continue
			}
			if selected != "" {
				return true, errConfigurationStale
			}
			selected = site.path
		}

		if selected == "" {
			return true, errConfigurationStale
		}
		switch op.Phase {
		case "Prepared":
			if err := r.a.revalidateFabricVLANSSIDOperation(recoveryCtx, config, source, selected, op, "Prepared"); err != nil {
				return true, err
			}
			permit, err := r.a.claimConfigurationGroup(recoveryCtx)
			if err != nil {
				return true, err
			}
			_, err = r.a.dispatchFabricVLANSSIDs(recoveryCtx, config, source, selected, permit)
			return true, err
		case "Verifying":
			return true, r.a.recordFabricVLANSSIDApplication(recoveryCtx, config, op.ID, selected, members, authorize, time.Now())
		default:
			return true, r.a.beginFabricVLANSSIDVerification(recoveryCtx, config, source, selected, members, authorize, time.Now())
		}
	}
	if strings.HasPrefix(op.Resource, "fabric-wireless-multicast-update:") {
		plan, err := r.a.client.compileFabricConfiguration(source.raw)
		if err != nil {
			return true, err
		}
		executor := plan.configurationExecutor
		if mappings, ok := executor.(*fabricVLANSSIDConfiguration); ok {
			executor = mappings.foundation
		}
		multicast, ok := executor.(*fabricMulticastConfiguration)
		if !ok {
			return true, errConfigurationTransition
		}
		selected := ""
		for _, site := range multicast.desired {
			if site.intent.wireless == nil {
				return true, errConfigurationTransition
			}
			deps, err := r.a.client.wirelessMulticastDependencies(recoveryCtx, site.path)
			if err != nil {
				return true, err
			}
			if deps.state.fabricID != op.TargetID || deps.state.siteID != op.ParentID || fabricWirelessMulticastResource(site.path, *site.intent.wireless, deps) != op.Resource {
				continue
			}
			if selected != "" {
				return true, errConfigurationStale
			}
			selected = site.path
		}
		if selected == "" {
			return true, errConfigurationStale
		}
		switch op.Phase {
		case "Prepared":
			if err := r.a.revalidateFabricWirelessMulticastOperation(recoveryCtx, config, source, selected, op, "Prepared"); err != nil {
				return true, err
			}
			permit, err := r.a.claimConfigurationGroup(recoveryCtx)
			if err != nil {
				return true, err
			}
			_, err = r.a.dispatchFabricWirelessMulticast(recoveryCtx, config, source, selected, permit)
			return true, err
		case "Verifying":
			return true, r.a.recordFabricWirelessMulticastApplication(recoveryCtx, config, op.ID, selected, members, authorize, time.Now())
		default:
			return true, r.a.beginFabricWirelessMulticastVerification(recoveryCtx, config, source, selected, members, authorize, time.Now())
		}
	}
	if strings.HasPrefix(op.Resource, "fabric-port-assignment-create:") {
		model, err := parseFabricEdgeDeployment(source.raw)
		if err != nil {
			return true, err
		}
		intents := model.assignments
		var selected *fabricPortAssignmentDeploymentIntent
		selectedPort := ""
		for i := range intents {
			deps, err := r.a.client.fabricPortAssignmentDependencies(recoveryCtx, intents[i])
			if err != nil {
				return true, err
			}
			if deps.deviceID != op.TargetID || deps.fabricID != op.ParentID {
				continue
			}
			for _, row := range intents[i].assignments {
				port := *row.Interface
				if fabricPortAssignmentResource(deps, port) != op.Resource {
					continue
				}
				if selected != nil {
					return true, errConfigurationStale
				}
				selected = &intents[i]
				selectedPort = port
			}
		}
		if selected == nil {
			return true, errConfigurationStale
		}
		device, port := selected.device.inventory.reference.Name, selectedPort
		switch op.Phase {
		case "Prepared":
			if err := r.a.revalidateFabricPortAssignmentOperation(recoveryCtx, config, source, device, port, op, "Prepared"); err != nil {
				return true, err
			}
			permit, err := r.a.claimConfigurationGroup(recoveryCtx)
			if err != nil {
				return true, err
			}
			_, err = r.a.dispatchFabricPortAssignment(recoveryCtx, config, source, device, port, permit)
			return true, err
		case "Verifying":
			return true, r.a.recordFabricPortAssignmentApplication(recoveryCtx, config, op.ID, device, port, members, authorize, time.Now())
		default:
			return true, r.a.beginFabricPortAssignmentVerification(recoveryCtx, config, source, device, port, members, authorize, time.Now())
		}
	}
	if strings.HasPrefix(op.Resource, "fabric-port-channel-create:") {
		model, err := parseFabricEdgeDeployment(source.raw)
		if err != nil {
			return true, err
		}
		intents := model.channels
		var selected *fabricPortChannelDeploymentIntent
		for i := range intents {
			deps, err := r.a.client.fabricPortChannelDependencies(recoveryCtx, intents[i])
			if err != nil {
				return true, err
			}
			if deps.deviceID != op.TargetID || deps.fabricID != op.ParentID || fabricPortChannelResource(deps) != op.Resource {
				continue
			}
			if selected != nil {
				return true, errConfigurationStale
			}
			selected = &intents[i]
		}
		if selected == nil {
			return true, errConfigurationStale
		}
		device, id := selected.device.inventory.reference.Name, *selected.channel.ID
		switch op.Phase {
		case "Prepared":
			if err := r.a.revalidateFabricPortChannelOperation(recoveryCtx, config, source, device, id, op, "Prepared"); err != nil {
				return true, err
			}
			permit, err := r.a.claimConfigurationGroup(recoveryCtx)
			if err != nil {
				return true, err
			}
			_, err = r.a.dispatchFabricPortChannel(recoveryCtx, config, source, device, id, permit)
			return true, err
		case "Verifying":
			return true, r.a.recordFabricPortChannelApplication(recoveryCtx, config, op.ID, device, id, members, authorize, time.Now())
		default:
			return true, r.a.beginFabricPortChannelVerification(recoveryCtx, config, source, device, id, members, authorize, time.Now())
		}
	}
	if isPendingFabricEventOperation(op.Section, op.Resource) {
		// Task/event evidence only admits convergence verification. It must not
		// publish a completion receipt that claims device convergence.
		if op.Phase == "Verifying" {
			return true, errFabricEventConvergenceUnavailable
		}
		sites, err := declaredPendingFabricEventSites(source.raw)
		if err != nil {
			return true, err
		}
		var selected *fabricSiteIntent
		for _, site := range sites {
			snapshot, targets, err := r.a.client.pendingFabricEventTargets(recoveryCtx, r.a.statusReader, config.Namespace, site)
			if err != nil {
				return true, err
			}
			if _, err := pendingFabricEventJournalReceipt(site, op, snapshot, targets, time.Now()); err != nil {
				continue
			}
			if selected != nil {
				return true, errConfigurationStale
			}
			candidate := site
			selected = &candidate
		}
		if selected == nil {
			return true, errConfigurationStale
		}
		return true, r.a.beginPendingFabricEventVerification(recoveryCtx, config, source, *selected, members, authorize, time.Now())
	}
	_, l2, ip, intents, err := parseAllFabricHandoffDeployment(source.raw)
	if err != nil {
		return true, err
	}
	var recover func() error
	selectRecovery := func(target, parent, resource string, begin, record, submit func() error) error {
		if target != op.TargetID || parent != op.ParentID || resource != op.Resource {
			return nil
		}
		if recover != nil {
			return errConfigurationStale
		}
		recover = begin
		if op.Phase == "Prepared" {
			recover = submit
		}
		if op.Phase == "Verifying" {
			recover = record
		}
		return nil
	}
	for _, intent := range intents {
		if !strings.HasPrefix(op.Resource, "fabric-sda-handoff-create:") {
			continue
		}

		deps, err := r.a.client.fabricSDAHandoffDependencies(recoveryCtx, intent)
		if err != nil {
			return true, err
		}
		key := intent.handoff
		if err := selectRecovery(deps.binding.desired.DeviceID, deps.binding.desired.FabricID, fabricSDAHandoffResource(deps),
			func() error {
				return r.a.beginFabricSDAHandoffVerification(recoveryCtx, config, source, key, members, authorize, time.Now())
			},
			func() error {
				return r.a.recordFabricSDAHandoffApplication(recoveryCtx, config, op.ID, key, members, authorize, time.Now())
			}, func() error {
				if err := r.a.revalidateFabricSDAHandoffOperation(recoveryCtx, config, source, key, op, "Prepared"); err != nil {
					return err
				}
				permit, err := r.a.claimConfigurationGroup(recoveryCtx)
				if err != nil {
					return err
				}
				_, err = r.a.dispatchFabricSDAHandoff(recoveryCtx, config, source, key, permit)
				return err
			}); err != nil {
			return true, err
		}
	}
	for _, intent := range ip {
		if !strings.HasPrefix(op.Resource, "fabric-l3-ip-handoff-create:") {
			continue
		}

		deps, err := r.a.client.fabricL3IPHandoffDependencies(recoveryCtx, intent.handoff, intent.device)
		if err != nil {
			return true, err
		}
		key := intent.handoff
		if err := selectRecovery(deps.binding.desired.DeviceID, deps.binding.desired.FabricID, fabricL3IPHandoffResource(deps),
			func() error {
				return r.a.beginFabricL3IPHandoffVerification(recoveryCtx, config, source, key, members, authorize, time.Now())
			},
			func() error {
				return r.a.recordFabricL3IPHandoffApplication(recoveryCtx, config, op.ID, key, members, authorize, time.Now())
			}, func() error {
				if err := r.a.revalidateFabricL3IPHandoffOperation(recoveryCtx, config, source, key, op, "Prepared"); err != nil {
					return err
				}
				permit, err := r.a.claimConfigurationGroup(recoveryCtx)
				if err != nil {
					return err
				}
				_, err = r.a.dispatchFabricL3IPHandoff(recoveryCtx, config, source, key, permit)
				return err
			}); err != nil {
			return true, err
		}
	}
	for _, intent := range l2 {
		if !strings.HasPrefix(op.Resource, "fabric-l2-handoff-create:") {
			continue
		}
		prerequisites, err := r.a.client.fabricL2HandoffPrerequisites(recoveryCtx, intent)
		if err != nil {
			return true, err
		}
		deps, err := r.a.client.fabricL2HandoffDependencies(recoveryCtx, intent.handoff, intent.device.inventory.reference, intent.device.fabricSite)
		if err != nil {
			return true, err
		}
		key := intent.handoff
		if err := selectRecovery(deps.binding.desired.DeviceID, deps.binding.desired.FabricID, fabricL2HandoffResource(deps)+":"+prerequisites.digest,
			func() error {
				return r.a.beginFabricL2HandoffVerification(recoveryCtx, config, source, key, members, authorize, time.Now())
			},
			func() error {
				return r.a.recordFabricL2HandoffApplication(recoveryCtx, config, op.ID, key, members, authorize, time.Now())
			}, func() error {
				if err := r.a.revalidateFabricL2HandoffOperation(recoveryCtx, config, source, key, op, "Prepared"); err != nil {
					return err
				}
				permit, err := r.a.claimConfigurationGroup(recoveryCtx)
				if err != nil {
					return err
				}
				_, err = r.a.dispatchFabricL2Handoff(recoveryCtx, config, source, key, permit)
				return err
			}); err != nil {
			return true, err
		}
	}
	if recover == nil {
		return true, errConfigurationStale
	}
	return true, recover()
}
