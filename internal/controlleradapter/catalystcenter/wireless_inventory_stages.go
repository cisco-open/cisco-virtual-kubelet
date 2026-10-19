// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
)

// wirelessInventoryStages adds dependency sequencing without another journal
// or task loop. Prerequisites retain their existing group resource prefixes;
// provisioning retains its baseline-encoded identity for durable recovery.
// validate must corroborate the complete prospective dependency set before any
// write, including prerequisites; nil is deliberately not permissive.
type wirelessInventoryStages struct {
	prerequisites configurationExecutor
	provisioning  []configurationExecutor
	validate      func(context.Context) error
}

func (s *wirelessInventoryStages) count() int {
	n := 0
	if s.prerequisites != nil {
		n += s.prerequisites.count()
	}
	for _, executor := range s.provisioning {
		if executor != nil {
			n += executor.count()
		}
	}
	return n
}
func (s *wirelessInventoryStages) next(ctx context.Context) (*configurationWrite, error) {
	if s.validate == nil || s.prerequisites == nil || len(s.provisioning) == 0 {
		return nil, errConfigurationTransition
	}
	for _, executor := range s.provisioning {
		if executor == nil {
			return nil, errConfigurationTransition
		}
	}
	if err := s.validate(ctx); err != nil {
		return nil, err
	}
	write, err := s.prerequisites.next(ctx)
	if err != nil || write != nil {
		return write, err
	}
	for _, executor := range s.provisioning {
		write, err := executor.next(ctx)
		if err != nil || write != nil {
			return write, err
		}
	}
	return nil, nil
}
func (s *wirelessInventoryStages) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if !strings.HasPrefix(op.Resource, wirelessProvisionRecordPrefix) {
		if s.prerequisites == nil {
			return false, errConfigurationTransition
		}
		return s.prerequisites.verify(ctx, op)
	}
	// Select exactly one executor by the durable name digest. Never try every
	// executor against a receipt or suppress an error from the actual owner.
	record, err := decodeWirelessProvisionRecord(op.Resource)
	if err != nil {
		return false, err
	}
	var matched *wirelessProvisionExecutor
	for _, executor := range s.provisioning {
		provision, ok := executor.(*wirelessProvisionExecutor)
		if !ok || provision == nil {
			return false, errConfigurationTransition
		}
		key, err := wirelessTagDigest(provision.reference.Name)
		if err != nil {
			return false, err
		}
		if key != record.NameKey {
			continue
		}
		if matched != nil {
			return false, errConfigurationOwned
		}
		matched = provision
	}
	if matched == nil {
		return false, errConfigurationStale
	}
	return matched.verify(ctx, op)
}

var _ configurationExecutor = (*wirelessInventoryStages)(nil)
