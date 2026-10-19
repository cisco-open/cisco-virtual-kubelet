// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// Rechecked on both sides of authorization. Retained, even released, members
// still need their original receipt to retire and therefore fence replacement.
func validateRetiredGroupReceipt(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, old, next *cvk.NetworkControllerConfigurationActionApplication, now time.Time) error {
	reject := errors.New("prior group receipt lacks retirement evidence")
	a, b := &old.Operation, &next.Operation
	if a.Phase != "Verified" || ValidateGroupJournalIdentity(a) != nil || a.ConfigUID != b.ConfigUID || a.ConfigName != b.ConfigName || a.Section != b.Section || a.TargetID != b.TargetID || a.ParentID != b.ParentID || a.ID == b.ID || a.TaskID == "" || a.SubmittedAt == nil || a.SubmittedAt.IsZero() || old.VerifiedAt == nil || old.VerifiedAt.IsZero() || old.VerifiedAt.Before(a.SubmittedAt) || old.VerifiedAt.After(now) || b.SubmittedAt == nil || old.VerifiedAt.After(b.SubmittedAt.Time) {
		return reject
	}
	for _, target := range a.PhysicalGroup.Targets {
		name := groupMemberReservationName(string(nc.UID), a.ID, a.PhysicalGroup.Digest, target.PhysicalIdentity)
		var retained ops.ControllerConfigurationReservation
		err := reader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: name}, &retained)
		if err == nil {
			return reject
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
	}
	// Also reject an unexpected name carrying the old operation identity.
	var list ops.ControllerConfigurationReservationList
	if err := reader.List(ctx, &list, client.InNamespace(nc.Namespace)); err != nil {
		return err
	}
	for _, member := range list.Items {
		if member.Spec.Controller.UID == string(nc.UID) && member.Spec.OperationID == a.ID {
			return reject
		}
	}
	return nil
}
