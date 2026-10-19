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
	"slices"
	"sync/atomic"
)

type authConfigurationExecutor struct {
	client  *client
	desired []authProfile
	// Fabric callers must supply their own scope/authority observation. Global
	// templates retain the empty fabric scope and use a versioned result binding.
	fabricID string
	guard    func(context.Context) error
	// A group dispatch permit is consumed exactly once, after all repeated
	// observations and immediately before the no-retry request.
	beforeSubmit func(context.Context) error
}

func (e *authConfigurationExecutor) count() int { return len(e.desired) }
func authACLManagedEqual(w, g *authACL) bool {
	if w == nil || g == nil {
		return w == nil && g == nil
	}
	if !reflect.DeepEqual(w.Enabled, g.Enabled) || !reflect.DeepEqual(w.ImplicitAction, g.ImplicitAction) || w.Contracts == nil || g.Contracts == nil {
		return false
	}
	a, b := slices.Clone(*w.Contracts), slices.Clone(*g.Contracts)
	less := func(a, b authContract) int {
		if a.Port < b.Port {
			return -1
		}
		if a.Port > b.Port {
			return 1
		}
		return 0
	}
	slices.SortFunc(a, less)
	slices.SortFunc(b, less)
	return slices.Equal(a, b)
}
func authProfilesEqual(w, g authProfile) bool {
	return reflect.DeepEqual(w.Order, g.Order) && reflect.DeepEqual(w.Timeout, g.Timeout) && reflect.DeepEqual(w.Wake, g.Wake) && reflect.DeepEqual(w.Hosts, g.Hosts) && (w.BPDU == nil || reflect.DeepEqual(w.BPDU, g.BPDU)) && (w.Name != "Low Impact" || authACLManagedEqual(w.ACL, g.ACL))
}

func authPreservedFieldsEqual(a, b authProfile) bool {
	return reflect.DeepEqual(a.Voice, b.Voice) && reflect.DeepEqual(a.BPDU, b.BPDU) && (a.Name != "Low Impact" || a.ACL != nil && b.ACL != nil && reflect.DeepEqual(a.ACL.Description, b.ACL.Description))
}

func (e *authConfigurationExecutor) observe(ctx context.Context) (map[string]authProfile, error) {
	if e.fabricID != "" {
		if e.guard == nil {
			return nil, errConfigurationDeviceAdmission
		}
		if err := e.guard(ctx); err != nil {
			return nil, err
		}
	}
	profiles, err := e.client.listAuthProfiles(ctx, e.fabricID)
	if err != nil {
		return nil, err
	}
	byName := map[string]authProfile{}
	for _, p := range profiles {
		byName[p.Name] = p
	}
	for _, w := range e.desired {
		p, ok := byName[w.Name]
		if !ok || !validAPIID(p.ID) {
			return nil, errors.New("built-in authentication profile identity unavailable")
		}
	}
	return byName, nil
}
func (e *authConfigurationExecutor) next(ctx context.Context) (*configurationWrite, error) {
	// Copy pointer-backed values and contract slices; callers cannot retarget a
	// compiled request or change its unmanaged-field preservation after planning.
	rawDesired, err := json.Marshal(e.desired)
	if err != nil {
		return nil, err
	}
	frozen := &authConfigurationExecutor{client: e.client, fabricID: e.fabricID, guard: e.guard, beforeSubmit: e.beforeSubmit}
	if json.Unmarshal(rawDesired, &frozen.desired) != nil {
		return nil, errConfigurationTransition
	}
	observed, err := frozen.observe(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range frozen.desired {
		g := observed[w.Name]
		if authProfilesEqual(w, g) {
			continue
		}
		// The shared update API accepts a list with the existing profile UUID.
		// Omit fabric for globals and omit irrelevant ACLs instead of sending null.
		item := map[string]any{"id": g.ID, "authenticationProfileName": w.Name, "authenticationOrder": *w.Order, "dot1xToMabFallbackTimeout": *w.Timeout, "wakeOnLan": *w.Wake, "numberOfHosts": *w.Hosts}
		if frozen.fabricID != "" {
			item["fabricId"] = frozen.fabricID
			if w.Name == "Closed Authentication" {
				bpdu := w.BPDU
				if bpdu == nil {
					bpdu = g.BPDU
				}
				if bpdu != nil {
					item["isBpduGuardEnabled"] = bpdu
				}
			}
		}
		// Voice VLAN is native metadata outside the pinned NaC input model.
		// Preserve a present value without inventing an absent default.
		if g.Voice != nil {
			item["isVoiceVlanEnabled"] = g.Voice
		}
		if w.Name == "Low Impact" {
			acl := *w.ACL
			acl.Description = g.ACL.Description
			item["preAuthAcl"] = &acl
		}
		body, err := json.Marshal([]any{item})
		if err != nil {
			return nil, err
		}
		var used atomic.Bool
		resource := w.Name
		if frozen.fabricID == "" {
			resource = globalAuthOperationResource(w, g)
		}
		return &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: g.ID, parent: frozen.fabricID}, submit: func(ctx context.Context) (Task, error) {
			if !used.CompareAndSwap(false, true) {
				return Task{}, errConfigurationTransition
			}
			for range 2 {
				fresh, err := frozen.observe(ctx)
				if err != nil {
					return Task{}, err
				}
				for _, want := range frozen.desired {
					a, b := observed[want.Name], fresh[want.Name]
					if a.ID != b.ID || a.FabricID != b.FabricID || !authProfilesEqual(a, b) || !authPreservedFieldsEqual(a, b) {
						return Task{}, errConfigurationStale
					}
				}
			}
			if frozen.beforeSubmit != nil {
				if err := frozen.beforeSubmit(ctx); err != nil {
					return Task{}, err
				}
			}
			raw, err := frozen.client.mutate(ctx, http.MethodPut, authProfilesPath, body)
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
		}}, nil
	}
	return nil, nil
}
func (e *authConfigurationExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Update" || op.ParentID != e.fabricID || !validAPIID(op.TargetID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	var selected *authConfigurationExecutor
	var result []string
	if e.fabricID == "" && !legacyGlobalAuthResource(op.Resource) {
		var err error
		result, err = parseGlobalAuthOperationResource(op.Resource)
		if err != nil {
			return false, err
		}
	}
	for _, w := range e.desired {
		if w.Name == op.Resource || result != nil && result[0] == authConfigurationValueDigest(w.Name) {
			if selected != nil {
				return false, errConfigurationTransition
			}
			selected = &authConfigurationExecutor{client: e.client, desired: []authProfile{w}, fabricID: e.fabricID, guard: e.guard}
		}
	}
	if selected == nil {
		if e.fabricID == "" {
			return false, errGlobalAuthBinding
		}
		return false, errConfigurationTransition
	}
	for range 2 {
		observed, err := selected.observe(ctx)
		if err != nil {
			return false, err
		}
		for _, w := range selected.desired {
			g := observed[w.Name]
			if g.ID != op.TargetID {
				if e.fabricID == "" {
					return false, errGlobalAuthBinding
				}
				return false, errors.New("authentication profile identity changed")
			}
			if !authProfilesEqual(w, g) {
				return false, nil
			}
			if result != nil && authProfileConfigurationDigest(g) != result[2] {
				return false, errGlobalAuthBinding
			}
		}
	}
	return true, nil
}
