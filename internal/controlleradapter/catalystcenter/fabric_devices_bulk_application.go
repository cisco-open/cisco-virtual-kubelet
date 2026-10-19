// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// Native memberships/tasks are necessary but not independent device proof. This
// private bridge does not enable public dispatch or manufacture that proof.
func (c *client) verifiedFabricDevicesBulkReceipt(ctx context.Context, reader ctrlclient.Reader, namespace string, wants []fabricDeviceIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time, proof configurationGroupConvergenceProof) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if c == nil || reader == nil || namespace == "" || proof == nil || op == nil || op.Phase != "Verifying" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationDeviceAdmission
	}
	count, mask, ids, err := parseFabricDevicesBulkResourceIDs(op.Resource, op.Action)
	if err != nil || count != len(wants) {
		return nil, errConfigurationStale
	}
	collect := func(ctx context.Context) (*fabricDevicesBulkSnapshot, []contract.GroupTargetIdentity, time.Time, error) {
		completed, err := c.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		done, err := c.verifyFabricDevicesBulk(ctx, wants, op)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		if !done {
			return nil, nil, time.Time{}, errConfigurationBusy
		}
		native, targets, err := c.fabricDevicesBulkTargets(ctx, reader, namespace, wants)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		resource := native.legacyResource(mask)
		if ids != nil {
			resource = native.resource(mask)
		}
		if native.deferredParent || native.mask != 0 || native.fabric != op.ParentID || resource != op.Resource {
			return nil, nil, time.Time{}, errConfigurationStale
		}
		return native, targets, completed, nil
	}
	native, err := observeConfigurationGroupConvergence(ctx, op, now, collect, proof)
	if err != nil {
		return nil, err
	}
	incoming := []fabricDeviceConfiguration{}
	for _, entry := range native.entries {
		incoming = append(incoming, entry.native)
	}
	// Exclude task/nonce/mask and transient pre-creation record IDs. Include all
	// incoming desired roles and every incoming/peer/parent physical incarnation.
	raw, err := json.Marshal(struct {
		Fabric   string
		Incoming []fabricDeviceConfiguration
		Targets  []contract.GroupTargetIdentity
	}{native.fabric, incoming, op.PhysicalGroup.Targets})
	if err != nil {
		return nil, err
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: fmt.Sprintf("fabric-devices-bulk:%x", sha256.Sum256(raw)), Operation: *verified, VerifiedAt: &at}, nil
}

func (a *adapter) recordFabricDevicesBulkApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, proof configurationGroupConvergenceProof, now time.Time) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 || !configurationOperationID.MatchString(operationID) || authorize == nil || proof == nil || now.IsZero() || !a.configurationGroupAdmissionReady() {
		return errConfigurationDeviceAdmission
	}
	// Keep the expected source identity immutable across external proof callbacks.
	config = config.DeepCopy()
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation
	if op.ID != operationID || op.Phase != "Verifying" || op.Section != "fabric" || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return errConfigurationStale
	}
	wants, err := a.retainedFabricDevicesBulk(ctx, source, op)
	if err != nil {
		return err
	}
	guardedProof := func(ctx context.Context, operation *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity, completed time.Time) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		if err := proof(ctx, operation, targets, completed); err != nil {
			return err
		}
		return a.checkConfigurationSnapshot(ctx, config, source)
	}
	var receipt *cvk.NetworkControllerConfigurationActionApplication
	ready, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		var err error
		receipt, err = a.client.verifiedFabricDevicesBulkReceipt(ctx, a.statusReader, config.Namespace, wants, op, now, guardedProof)
		return err == nil, err
	})
	if err != nil {
		return err
	}
	if !ready || receipt == nil {
		return errConfigurationBusy
	}

	var previous *cvk.NetworkControllerConfigurationActionApplication
	for i := range nc.Status.Configuration.ActionApplications {
		old := &nc.Status.Configuration.ActionApplications[i]
		if old.Section == receipt.Section && old.Resource == receipt.Resource {
			if previous != nil {
				return errConfigurationOwned
			}
			previous = old.DeepCopy()
		}
	}
	if previous != nil {
		return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, authorize, now, previous)
	}
	return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, authorize, now)
}
