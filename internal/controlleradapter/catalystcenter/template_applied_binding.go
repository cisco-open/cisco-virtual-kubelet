// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (a *adapter) recordTemplateApplied(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, e *templateOwnedExecutor, op *cvk.NetworkControllerConfigurationOperation) error {
	if _, _, ok := e.findApplied(op); !ok || op.Phase != "Verifying" || op.ConfigUID != e.owner || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
		return errConfigurationStale
	}
	if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
		return err
	}
	if err := e.retainedStateMatches(current.Status.Configuration); err != nil {
		return err
	}
	if err := templateApplyCapacity(current.Status.Configuration, op); err != nil {
		return err
	}
	original, err := templateApplyCreator(current.Status.Configuration, op)
	if err != nil {
		return err
	}
	complete, err := e.verifyApplied(ctx, op)
	if err != nil {
		return err
	}
	if !complete {
		return errConfigurationBusy
	}
	version, bodyMAC := "", ""
	if templateVersionOperation(op) {
		version, bodyMAC, err = e.captureCreatedVersion(ctx, op)
		if err != nil {
			return err
		}
	}
	mac, err := e.captureAppliedBaseline(ctx, op)
	if err != nil {
		return err
	}
	complete, err = e.verifyApplied(ctx, op)
	if err != nil {
		return err
	}
	if !complete {
		return errConfigurationBusy
	}
	if templateVersionOperation(op) {
		again, againMAC, err := e.captureCreatedVersion(ctx, op)
		if err != nil {
			return err
		}
		if again != version || !hmac.Equal([]byte(againMAC), []byte(bodyMAC)) {
			return errConfigurationStale
		}
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	// CAS the same journal/owner/history snapshot. A concurrent receipt change
	// cannot be overwritten by fresh native proof computed for the earlier one.
	current.Status.Configuration.Operation.Phase = "Verified"
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if op.SubmittedAt.After(now.Time) {
		now = *op.SubmittedAt.DeepCopy()
	}
	result := templateAppliedResult(op, original, mac)
	if templateVersionOperation(op) {
		result = templateAppliedVersionResult(op, original, mac, version, bodyMAC)
	}
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: result, Operation: *current.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &now}
	if err := templateReplaceApplied(current.Status.Configuration, row); err != nil {
		return err
	}
	return a.persistConfigurationActionReceipt(ctx, current)
}
