// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
)

type ipamConfigurationExecutor struct {
	prune           bool
	submissionGuard func(context.Context, string, string, configurationResourceIdentity) error
	owner           string
	receipts        []ciskov1.NetworkControllerConfigurationActionApplication
	client          *client
	intent          networkReportIntent
}

// The exact merged payload is request-local evidence, never persisted in the
// shared journal. Replanning protects fields retained from native observation.
type ipamWritePlan struct {
	write   *configurationWrite
	payload string
}

func (e *ipamConfigurationExecutor) count() int {
	return len(e.intent.pools) + len(e.intent.reservations)
}
func ipamPoolIdentity(name string) string              { return "ip-pool:" + name }
func ipamReservationIdentity(site, name string) string { return "ip-reservation:" + site + "#" + name }

// Only explicitly desired optional fields are replaced. Retain all others from
// the authoritative observation; do not turn unspecified into JSON null.
func ipamSpacePayload(want, observed *poolAddressSpace) map[string]any {
	merged := *want
	if observed != nil {
		if merged.Gateway == nil {
			merged.Gateway = observed.Gateway
		}
		if merged.DHCP == nil {
			merged.DHCP = observed.DHCP
		}
		if merged.DNS == nil {
			merged.DNS = observed.DNS
		}
		if merged.SLAAC == nil {
			merged.SLAAC = observed.SLAAC
		}
	}
	out := map[string]any{"subnet": merged.Subnet, "prefixLength": *merged.PrefixLength}
	if merged.Gateway != nil {
		out["gatewayIpAddress"] = *merged.Gateway
	}
	if merged.DHCP != nil {
		out["dhcpServers"] = append([]string{}, (*merged.DHCP)...)
	}
	if merged.DNS != nil {
		out["dnsServers"] = append([]string{}, (*merged.DNS)...)
	}
	if merged.SLAAC != nil {
		out["slaacSupport"] = *merged.SLAAC
	}
	if merged.GlobalPoolID != "" {
		out["globalPoolId"] = merged.GlobalPoolID
	}
	return out
}
func (e *ipamConfigurationExecutor) write(resource, id, parent, path string, payload any) (*ipamWritePlan, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	action, method := "Create", http.MethodPost
	if id != "" {
		if !validAPIID(id) {
			return nil, errInvalidResponse
		}
		action, method = "Update", http.MethodPut
		path += "/" + id
	}
	write := &configurationWrite{resource: resource, action: action, identity: configurationResourceIdentity{target: id, parent: parent}, submit: func(ctx context.Context) (Task, error) {
		return e.client.ipamMutationTask(ctx, method, path, body)
	}}
	return &ipamWritePlan{write: write, payload: string(body)}, nil
}
func (c *client) ipamMutationTask(ctx context.Context, method, path string, body []byte) (Task, error) {
	raw, err := c.mutate(ctx, method, path, body)
	if err != nil {
		return Task{}, err
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		return Task{}, errInvalidResponse
	}
	return Task{ID: receipt.Response.ID}, nil
}
func (e *ipamConfigurationExecutor) observe(ctx context.Context) ([]ipPool, []ipReservation, map[string]Site, error) {
	pools, err := e.client.listIPPools(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	reservations, err := e.client.listIPReservations(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = validateIPAMAllocation(e.intent, pools, reservations); err != nil {
		return nil, nil, nil, err
	}
	sites := map[string]Site{}
	if len(e.intent.reservations) > 0 {
		list, err := e.client.ListSites(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, s := range list {
			sites[s.NameHierarchy] = s
		}
		for _, r := range e.intent.reservations {
			s, ok := sites[r.site]
			if !ok || s.Type != r.siteKind || !validAPIID(s.ID) {
				return nil, nil, nil, errors.New("IPAM reservation site is missing or incompatible")
			}
			for _, old := range reservations {
				if old.Name == r.Name && old.SiteID != s.ID {
					return nil, nil, nil, errors.New("IPAM reservation relocation is not supported")
				}
			}
		}
	}
	if err := e.checkOwnership(pools, reservations); err != nil {
		return nil, nil, nil, err
	}
	return pools, reservations, sites, nil
}
func (e *ipamConfigurationExecutor) next(ctx context.Context) (*configurationWrite, error) {
	plan, err := e.executionPlan(ctx)
	if err != nil || plan == nil {
		return nil, err
	}
	write := *plan.write
	if e.owner != "" && !ipamOwnedSelected(write.resource) {
		receipt, err := e.ipamReceipt(plan.write.resource)
		if err != nil {
			return nil, err
		}
		// Preserve legacy imported updates without fabricating resource ownership.
		if write.action == "Create" || receipt != nil {
			write.resource = ipamOwnedResource(plan.write.resource)
		}
	}
	var used atomic.Bool
	write.submit = func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		current, err := e.executionPlan(ctx)
		if err != nil {
			return Task{}, err
		}
		if current == nil || current.write.resource != plan.write.resource || current.write.action != plan.write.action || !reflect.DeepEqual(current.write.identity, plan.write.identity) || current.payload != plan.payload {
			return Task{}, errConfigurationStale
		}
		if ipamOwnedSelected(write.resource) && e.submissionGuard != nil {
			if err := e.submissionGuard(ctx, write.resource, write.action, write.identity); err != nil {
				return Task{}, err
			}
		}
		return plan.write.submit(ctx)
	}
	return &write, nil
}

func (e *ipamConfigurationExecutor) plan(ctx context.Context) (*ipamWritePlan, error) {
	pools, reservations, sites, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]ipPool{}
	for _, p := range pools {
		byName[p.Name] = p
	}
	var next *ipamWritePlan
	for _, want := range e.intent.pools {
		got, exists := byName[want.Name]
		changed := !exists
		if exists {
			fields, err := poolSpaceDiff(want.Space, got.Space)
			if err != nil {
				return nil, err
			}
			changed = len(fields) > 0
		}
		if changed && next == nil {
			next, err = e.write(ipamPoolIdentity(want.Name), got.ID, "", globalPoolsPath, map[string]any{"name": want.Name, "poolType": want.Type, "addressSpace": ipamSpacePayload(want.Space, got.Space)})
			if err != nil {
				return nil, err
			}
		}
	}
	for _, want := range e.intent.reservations {
		var got ipReservation
		for _, r := range reservations {
			if r.Name == want.Name {
				got = r
				break
			}
		}
		payload := map[string]any{"name": want.Name, "poolType": want.Type, "siteId": sites[want.site].ID}
		changed, ready := got.ID == "", true
		for i, w := range []*poolAddressSpace{want.V4, want.V6} {
			old, key := got.V4, "ipV4AddressSpace"
			if i == 1 {
				old, key = got.V6, "ipV6AddressSpace"
			}
			if w == nil {
				if old != nil {
					payload[key] = ipamSpacePayload(old, nil)
				}
				continue
			}
			parent, ok := byName[w.GlobalPoolID]
			if !ok {
				ready = false
				continue
			}
			resolved := *w
			resolved.GlobalPoolID = parent.ID
			fields, err := poolSpaceDiff(w, old)
			if err != nil {
				return nil, err
			}
			changed = changed || len(fields) > 0
			payload[key] = ipamSpacePayload(&resolved, old)
		}
		if changed && ready && next == nil {
			next, err = e.write(ipamReservationIdentity(want.site, want.Name), got.ID, sites[want.site].ID, sitePoolsPath, payload)
			if err != nil {
				return nil, err
			}
		}
	}
	return next, nil
}
func (e *ipamConfigurationExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.Action != "Create" && op.Action != "Update" {
		return false, errConfigurationTransition
	}
	if op.Action == "Update" && !validAPIID(op.TargetID) {
		return false, errConfigurationTransition
	}
	pools, reservations, sites, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, w := range e.intent.pools {
		if ipamPoolIdentity(w.Name) == op.Resource {
			if op.ParentID != "" {
				return false, errConfigurationTransition
			}
			for _, g := range pools {
				if g.Name == w.Name {
					if op.TargetID != "" && op.TargetID != g.ID {
						return false, errors.New("IPAM pool identity changed")
					}
					fields, err := poolSpaceDiff(w.Space, g.Space)
					return len(fields) == 0, err
				}
			}
			return false, nil
		}
	}
	byName := map[string]ipPool{}
	for _, p := range pools {
		byName[p.Name] = p
	}
	for _, w := range e.intent.reservations {
		if ipamReservationIdentity(w.site, w.Name) == op.Resource {
			if !validAPIID(op.ParentID) || sites[w.site].ID != op.ParentID {
				return false, errors.New("IPAM reservation site identity changed")
			}
			for _, g := range reservations {
				if g.Name == w.Name {
					if op.TargetID != "" && op.TargetID != g.ID {
						return false, errors.New("IPAM reservation identity changed")
					}
					for i, s := range []*poolAddressSpace{w.V4, w.V6} {
						if s == nil {
							continue
						}
						old := g.V4
						if i == 1 {
							old = g.V6
						}
						if old == nil || byName[s.GlobalPoolID].ID != old.GlobalPoolID {
							return false, nil
						}
						fields, err := poolSpaceDiff(s, old)
						if err != nil || len(fields) > 0 {
							return false, err
						}
					}
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, errConfigurationTransition
}

// Compose resource semantics only; the controller still has one journal and
// one submission permit. Every selected executor observes before any write.
type networkSectionExecutor struct {
	assignments *siteCredentialExecutor
	settings    *networkConfigurationExecutor
	ipam        *ipamConfigurationExecutor
}

func (e *networkSectionExecutor) count() int {
	return e.settings.count() + e.ipam.count() + e.assignments.count()
}
func (e *networkSectionExecutor) next(ctx context.Context) (*configurationWrite, error) {
	return e.nextWithCredentialDefinitions(ctx, nil)
}
func (e *networkSectionExecutor) nextWithCredentialDefinitions(ctx context.Context, definitions map[string]bool) (*configurationWrite, error) {
	var first *configurationWrite
	if e.ipam.count() > 0 {
		m, err := e.ipam.next(ctx)
		if err != nil {
			return nil, err
		}
		first = m
	}
	if e.settings.count() > 0 {
		m, err := e.settings.next(ctx)
		if err != nil {
			return nil, err
		}
		if first == nil {
			first = m
		}
	}
	if e.assignments.count() > 0 {
		m, err := e.assignments.nextWithDefinitions(ctx, definitions)
		if err != nil {
			return nil, err
		}
		if first == nil {
			first = m
		}
	}
	return first, nil
}
func (e *networkSectionExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if strings.HasPrefix(op.Resource, "ip-pool:") || strings.HasPrefix(op.Resource, "ip-reservation:") {
		return e.ipam.verify(ctx, op)
	}
	if strings.HasPrefix(op.Resource, "credential-assignment:") {
		return e.assignments.verify(ctx, op)
	}
	return e.settings.verify(ctx, op)
}
