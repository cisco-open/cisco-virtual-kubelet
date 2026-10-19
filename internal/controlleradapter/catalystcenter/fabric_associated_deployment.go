// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"slices"
	"strings"
)

// This composition owns dependency ordering only. Existing executors retain
// native resource semantics and the shared journal retains task/recovery logic.
type fabricAssociatedDeployment struct {
	parents      configurationExecutor
	parentReport func(context.Context) ([]configapi.NetworkControllerDriftEntry, error)
	definitions  *fabricL3Executor
	associations *fabricL3AssociationExecutor
	l2           *fabricL2Executor
}

func (e *fabricAssociatedDeployment) count() int {
	return e.parents.count() + e.definitions.count() + e.associations.count() + e.l2.count()
}
func (e *fabricAssociatedDeployment) pending() map[fabricL2Key]bool {
	out := map[fabricL2Key]bool{}
	for _, w := range e.associations.desired {
		out[fabricL2Key{w.path, w.name}] = true
	}
	return out
}
func (e *fabricAssociatedDeployment) next(ctx context.Context) (*configurationWrite, error) {
	parent, err := e.parents.next(ctx)
	if err != nil {
		return nil, err
	}
	var definition *configurationWrite
	if e.definitions.count() > 0 {
		definition, err = e.definitions.next(ctx)
		if err != nil {
			return nil, err
		}
	}
	states, err := e.associations.observe(ctx)
	if err != nil {
		return nil, err
	}
	pending := e.pending()
	for _, s := range states {
		if s.network.ID == "" && !slices.Contains(e.definitions.names, s.want.name) {
			return nil, errConfigurationBusy
		}
		if s.want.parent != "" && !slices.Contains(s.network.Fabrics, s.parentFabricID) && !pending[fabricL2Key{s.want.parent, s.want.name}] {
			return nil, errConfigurationBusy
		}
	}
	if e.l2.count() > 0 {
		l2, err := e.l2.observeWithPending(ctx, pending)
		if err != nil {
			return nil, err
		}
		for _, s := range l2 {
			if s.exists && !fabricL2Matches(s.want, s.actual, s.vlan) {
				return nil, errConfigurationTransition
			}
		}
	}
	if parent != nil {
		return parent, nil
	}
	if definition != nil {
		return definition, nil
	}
	association, err := e.associations.next(ctx)
	if err != nil || association != nil {
		return association, err
	}
	if e.l2.count() > 0 {
		return e.l2.next(ctx)
	}
	return nil, nil
}
func (e *fabricAssociatedDeployment) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if strings.HasPrefix(op.Resource, "l3-virtual-network:") {
		return e.definitions.verify(ctx, op)
	}
	if strings.HasPrefix(op.Resource, "fabric-l3-member:") || strings.HasPrefix(op.Resource, "fabric-l2:") {
		parent, err := e.parents.next(ctx)
		if err != nil {
			return false, err
		}
		if parent != nil {
			return false, errConfigurationStale
		}
		if strings.HasPrefix(op.Resource, "fabric-l3-member:") {
			return e.associations.verify(ctx, op)
		}
		return e.l2.verify(ctx, op)
	}
	return e.parents.verify(ctx, op)
}
func (e *fabricAssociatedDeployment) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	out, err := e.parentReport(ctx)
	if err != nil {
		return nil, err
	}
	if e.definitions.count() > 0 {
		d, err := e.definitions.report(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	d, err := e.associations.report(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, d...)
	if e.l2.count() > 0 {
		d, err := e.l2.reportWithPending(ctx, e.pending())
		if err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	return out, nil
}
