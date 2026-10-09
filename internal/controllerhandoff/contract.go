// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// Package controllerhandoff contains only the credential-free delegation contract.
package controllerhandoff

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

const Finalizer = "ops.cisco.vk/swim-handoff-evidence"
const ControllerType = "catalyst-center"

func Name(uid string) string {
	h := sha256.Sum256([]byte(uid))
	return "swim-" + hex.EncodeToString(h[:16])
}
func ExecutionModel(up *ops.IOSXESoftwareUpgrade) ops.UpgradeExecutionModel {
	if up.Spec.ImageSource.CatalystCenter != nil {
		if up.Spec.ImageSource.CatalystCenter.StandardReloadProfile != "" {
			return ops.UpgradeExecutionModelCatalystCenterReloadV1
		}
		if up.Spec.ImageSource.CatalystCenter.Preparation != nil {
			return ops.UpgradeExecutionModelCatalystCenterPreparationV1
		}
		return ops.UpgradeExecutionModelCatalystCenterV1
	}
	return ops.UpgradeExecutionModelAtMostOnceV1
}
func KnownExecution(up *ops.IOSXESoftwareUpgrade) bool {
	return up.Status.ExecutionModel == ExecutionModel(up)
}
func ValidateSource(s *ops.CatalystCenterImageSource) error {
	if s == nil || s.ControllerName == "" || s.ControllerUID == "" {
		return fmt.Errorf("Catalyst Center requires a pinned controller name and UID")
	}
	if p := s.Preparation; p != nil && (p.Name == "" || p.UID == "" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(p.SHA256)) {
		return fmt.Errorf("preparation requires immutable policy name, UID and SHA-256")
	}
	if s.StandardReloadProfile != "" && s.StandardReloadProfile != "CatalystCenter323" {
		return fmt.Errorf("unsupported standard reload profile")
	}
	id := regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	if !id.MatchString(s.DeviceID) || !id.MatchString(s.ImageID) {
		return fmt.Errorf("invalid controller device/image ID")
	}
	if err := softwarelifecycle.ValidateTargetVersion(s.ImageVersion); err != nil {
		return err
	}
	if s.TransferFallbackAddress != "" {
		if _, err := netip.ParseAddr(s.TransferFallbackAddress); err != nil {
			return fmt.Errorf("fallback address must be a literal IP")
		}
	}
	return nil
}
