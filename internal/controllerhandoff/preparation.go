// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerhandoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PreparationPolicy deliberately supports only exact retired CVK OS archives.
// No paths, commands, globs, diagnostic files or application archives are accepted.
type PreparationPolicy struct {
	// DistributionReserveBytes covers both the incoming archive and extracted
	// packages in addition to the activation floor. It is not deducted for
	// cached images; a new immutable policy is required to change it.
	DistributionReserveBytes uint64 `json:"distributionReserveBytes,omitempty"`
	Profile                  string `json:"profile"`
	RequiredFreeBytes        uint64 `json:"requiredFreeBytes"`
	HeadroomBytes            uint64 `json:"headroomBytes"`
	MaxFiles                 int    `json:"maxFiles"`
	MaxBytes                 uint64 `json:"maxBytes"`
}

func ReadPreparationPolicy(ctx context.Context, reader client.Reader, namespace string, ref *ops.SWIMPreparationPolicyRef) (PreparationPolicy, error) {
	var p PreparationPolicy
	if ref == nil {
		return p, fmt.Errorf("missing preparation policy")
	}
	var cm core.ConfigMap
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &cm); err != nil {
		return p, err
	}
	if string(cm.UID) != ref.UID || cm.Immutable == nil || !*cm.Immutable || !cm.DeletionTimestamp.IsZero() || cm.Labels["ops.cisco.vk/swim-preparation-policy"] != "true" {
		return p, fmt.Errorf("preparation policy must be an immutable, labeled, pinned ConfigMap")
	}
	raw := []byte(cm.Data["policy.json"])
	if len(raw) == 0 || len(raw) > 4096 || fmt.Sprintf("%x", sha256.Sum256(raw)) != ref.SHA256 {
		return p, fmt.Errorf("preparation policy content changed or is invalid")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("invalid preparation policy JSON")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("trailing preparation policy content")
	}
	if p.Profile != "RetiredCVKImageArchivesV1" || p.DistributionReserveBytes > 16<<30 || p.RequiredFreeBytes == 0 || p.RequiredFreeBytes > 64<<30 || p.HeadroomBytes > 4<<30 || p.MaxFiles < 1 || p.MaxFiles > 3 || p.MaxBytes == 0 || p.MaxBytes > 16<<30 {
		return p, fmt.Errorf("unsupported or unbounded preparation policy")
	}
	return p, nil
}

// ForStage preserves the activation-space floor while reserving download and extraction bytes
// before distribution. Inputs have already passed the bounded policy decoder.
func (p PreparationPolicy) ForStage(stage string) (PreparationPolicy, error) {
	switch stage {
	case "ReadyToDistribute":
		p.RequiredFreeBytes += p.DistributionReserveBytes
	case "ReadyToActivate":
	default:
		return p, fmt.Errorf("invalid preparation stage")
	}
	return p, nil
}
