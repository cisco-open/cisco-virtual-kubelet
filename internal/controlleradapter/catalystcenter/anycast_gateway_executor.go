// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
)

type anycastGatewayExecutor struct {
	client    *client
	desired   []fabricAnycastIntent
	bulkGuard func(context.Context, string, string) error
}
type anycastGatewayState struct {
	want fabricAnycastIntent
	deps anycastGatewayDependencies
}

func (e *anycastGatewayExecutor) count() int { return len(e.desired) }
func (e *anycastGatewayExecutor) observe(ctx context.Context) ([]anycastGatewayState, error) {
	if len(e.desired) == 0 {
		return []anycastGatewayState{}, nil
	}
	snapshot, err := e.client.loadAnycastGatewayInventory(ctx, e.desired)
	if err != nil {
		return nil, err
	}
	pending := map[anycastGatewayKey]bool{}
	for _, w := range e.desired {
		state := snapshot.states[fabricL3AssociationIntent{path: w.path, parent: w.parent, name: w.network}]
		if !state.ready || !state.exists {
			return nil, errConfigurationBusy
		}
		key := anycastGatewayKey{state.fabricID, w.pool}
		if pending[key] {
			return nil, errInvalidResponse
		}
		pending[key] = true
	}
	out := []anycastGatewayState{}
	for _, w := range e.desired {
		deps, err := snapshot.resolveWithPending(w, pending)
		if err != nil {
			return nil, err
		}
		out = append(out, anycastGatewayState{w, deps})
	}
	return out, nil
}
func (e *anycastGatewayExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if e.bulkGuard != nil {
		return e.nextBulk(ctx)
	}
	states, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	waiting := false
	for _, s := range states {
		if !s.deps.exists {
			if err := validateAnycastCreateIntent(s.want); err != nil {
				return nil, err
			}
		}
		if s.deps.pendingParent {
			waiting = true
			continue
		}
		write, err := e.client.anycastGatewayWrite(s.want, s.deps)
		if err != nil {
			return nil, err
		}
		if first == nil && write != nil {
			first = write
		}
	}
	if first == nil && waiting {
		return nil, errConfigurationBusy
	}
	return first, nil
}
func (e *anycastGatewayExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if isAnycastBulkResource(op.Resource) {
		states, err := e.observe(ctx)
		if err != nil {
			return false, err
		}
		members := []fabricAnycastIntent{}
		for _, state := range states {
			if state.deps.fabricID == op.ParentID {
				members = append(members, state.want)
			}
		}
		return e.client.verifyAnycastGatewayBulk(ctx, members, op)
	}
	for _, w := range e.desired {
		if strings.HasPrefix(op.Resource, w.resourcePrefix()) {
			return e.client.verifyAnycastGateway(ctx, w, op)
		}
	}
	return false, errConfigurationTransition
}
func (e *anycastGatewayExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	states, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, s := range states {
		change := configapi.NetworkControllerDriftChangeAdd
		if s.deps.exists {
			matches, err := anycastGatewayMatches(s.want.anycastGatewayIntent, s.deps.actual, s.deps.pools, s.deps.parent)
			if err != nil {
				return nil, err
			}
			if matches {
				continue
			}
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: strings.TrimSuffix(s.want.resourcePrefix(), ":"), Path: "/fabric_sites/anycast_gateways", Change: change})
	}
	return out, nil
}
