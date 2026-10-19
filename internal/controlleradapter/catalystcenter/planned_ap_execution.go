// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

var errPlannedAPRadioReplacement = errors.New("planned AP radio changes require an explicit replacement lifecycle")

func plannedAPResource(w plannedAPIntent) string {
	raw, _ := json.Marshal([]string{w.floor, w.payload["name"].(string)})
	return fmt.Sprintf("planned-ap:%x", sha256.Sum256(raw))
}

// Merge selected attributes into complete readback. Radios are identified by
// their selected attributes, never by an unstable response array index.
// Unselected radio settings and optional position/MAC values are preserved.
func plannedAPUpdatePayload(want plannedAPIntent, got plannedAPObservation) (map[string]any, bool, error) {
	if err := validatePlannedAPObservation(&got); err != nil {
		return nil, false, err
	}
	if want.payload["name"] != got.Name {
		return nil, false, errConfigurationTransition
	}
	raw, _ := json.Marshal(got)
	var body, baseline map[string]any
	if json.Unmarshal(raw, &body) != nil || json.Unmarshal(raw, &baseline) != nil {
		return nil, false, errInvalidResponse
	}
	for key, value := range want.payload {
		switch key {
		case "position":
			for axis, n := range value.(map[string]any) {
				body["position"].(map[string]any)[axis] = n
			}
		case "radios":
			if !plannedAPRadiosMatch(value.([]any), got.Radios) {
				return nil, false, errPlannedAPRadioReplacement
			}

		default:
			body[key] = value
		}
	}
	changed := !reflect.DeepEqual(body, baseline)
	// Band information is read-only on edit; native radio IDs are retained.
	for _, value := range body["radios"].([]any) {
		delete(value.(map[string]any), "bands")
	}
	return body, changed, nil
}

// Match the pinned provider's optional-field omission. The controller supplies
// its own MAC, Z and radio defaults; CVK neither invents nor journals them.
// A nonempty radio collection is still required by the native creation API.
func validatePlannedAPCreate(w plannedAPIntent) error {
	position, ok := w.payload["position"].(map[string]any)
	if !ok || position["x"] == nil || position["y"] == nil {
		return errPlannedAPModel
	}
	radios, ok := w.payload["radios"].([]any)
	if !ok || len(radios) < 1 || len(radios) > 4 {
		return errPlannedAPModel
	}
	return nil
}

type plannedAPExecutor struct {
	client         *client
	desired        []plannedAPIntent
	retainIdentity bool
	identityOwner  string
	identities     map[string]plannedAPIdentityRecord
	dispatchGuard  func(context.Context, string, string, configurationResourceIdentity) error
	comparison     *templateInputComparison
	sourceMode     string
	applied        map[string]ciskov1.NetworkControllerConfigurationActionApplication
	floorEffects   map[string]plannedAPFloorAuthority
}
type plannedAPState struct {
	want    plannedAPIntent
	floorID string
	floor   Site
	actual  plannedAPObservation
	native  map[string]any
}

func (e *plannedAPExecutor) count() int { return len(e.desired) }
func (e *plannedAPExecutor) observe(ctx context.Context) ([]plannedAPState, error) {
	return e.observeMode(ctx, false)
}

func (e *plannedAPExecutor) observeMode(ctx context.Context, completion bool) ([]plannedAPState, error) {
	if err := e.client.validatePlannedAPModels(ctx, e.desired); err != nil {
		return nil, err
	}
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.checkFloorIdentities(sites); err != nil {
		return nil, err
	}
	floors := map[string]Site{}
	for _, site := range sites {
		if site.Type == "floor" {
			floors[site.NameHierarchy] = site
		}
	}
	inventories := map[string]map[string]plannedAPNativePosition{}
	out := []plannedAPState{}
	for _, want := range e.desired {
		floor := floors[want.floor]
		id := floor.ID
		if !validAPIID(id) {
			return nil, errInvalidResponse
		}
		inventory, seen := inventories[id]
		if !seen {
			inventory, err = e.client.plannedAPNativePositions(ctx, id)
			if err != nil {
				return nil, err
			}
			inventories[id] = inventory
		}
		row := inventory[want.payload["name"].(string)]
		actual := row.actual
		if record, exists := e.identities[plannedAPResource(want)]; exists && actual.ID != record.id {
			return nil, errConfigurationOwned
		}
		state := plannedAPState{want: want, floorID: id, floor: floor, actual: actual, native: row.native}
		if !completion {
			if err := e.checkPrivateBaseline(ctx, state); err != nil {
				return nil, err
			}
		}
		if actual.ID != "" && want.payload["type"] != actual.Type {
			// A type edit preserves native radios. Validate the complete retained
			// band/antenna set, not just the fields selected in the new source.
			if err := e.client.validatePlannedAPModels(ctx, []plannedAPIntent{plannedAPRetainedModel(want, actual)}); err != nil {
				return nil, err
			}
		}
		out = append(out, state)
	}
	return out, nil
}
func (e *plannedAPExecutor) next(ctx context.Context) (*configurationWrite, error) {
	return e.nextWithGeometry(ctx, nil)
}

// Future geometry is used only by the enclosing site planner's complete-source
// preflight. It returns site mutations first; actual placement dispatch always
// rechecks the then-current native geometry and exact floor identities.
func (e *plannedAPExecutor) nextWithGeometry(ctx context.Context, future []desiredSite) (*configurationWrite, error) {
	observations, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	positions := []plannedAPIntent{}
	floorIDs := map[string]string{}
	frozen, err := plannedAPDispatchView(observations)
	if err != nil {
		return nil, err
	}
	for _, o := range observations {
		body := o.want.payload
		changed := true
		action, suffix := "Create", "/bulk"
		if o.actual.ID != "" {
			body, changed, err = plannedAPUpdatePayload(o.want, o.actual)
			if err != nil {
				return nil, err
			}
			if changed {
				if err := validatePlannedAPUpdateFootprint(o.native); err != nil {
					return nil, err
				}
			}
			action, suffix = "Update", "/bulkChange"
		} else if err := validatePlannedAPCreate(o.want); err != nil {
			return nil, err
		}
		geometry, geometryErr := plannedAPObservedGeometry(o.floor)
		for _, site := range future {
			if site.path == o.want.floor {
				geometry, geometryErr = plannedAPDesiredGeometry(site)
				break
			}
		}
		if geometryErr != nil {
			return nil, geometryErr
		}
		if err := validatePlannedAPPosition(body, geometry); err != nil {
			return nil, err
		}
		positions = append(positions, frozenPlannedAPPosition(o.want.floor, body))
		floorIDs[o.want.floor] = o.floorID
		if !changed {
			continue
		}
		raw, err := json.Marshal([]any{body})
		if err != nil {
			return nil, errInvalidResponse
		}
		endpoint := plannedAPPositionsPath(o.floorID) + suffix
		resource := plannedAPResource(o.want)
		if e.retainIdentity && action == "Create" {
			resource = plannedAPObservePrefix + strings.TrimPrefix(resource, "planned-ap:")
		}
		if e.comparison != nil {
			if action == "Create" {
				resource = plannedAPPrivateObservePrefix + strings.TrimPrefix(plannedAPResource(o.want), "planned-ap:") + ":" + e.sourceMode + ":" + e.comparison.keyID
			} else {
				resource, err = e.privateUpdateResource(o, body)
				if err != nil {
					return nil, err
				}
			}
		}
		write := &configurationWrite{resource: resource, action: action, identity: configurationResourceIdentity{target: o.actual.ID, parent: o.floorID}, submit: func(ctx context.Context) (Task, error) {
			response, err := e.client.mutate(ctx, http.MethodPost, endpoint, raw)
			if err != nil {
				return Task{}, err
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(response, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				return Task{}, errInvalidResponse
			}
			return Task{ID: receipt.Response.ID}, nil
		}}
		guardConfigurationPreflight(write, func(ctx context.Context) error {

			if err := e.client.validatePlannedAPModels(ctx, e.desired); err != nil {
				return err
			}
			sites, err := e.client.ListSites(ctx)
			if err != nil {
				return err
			}
			current := map[string]Site{}
			for _, site := range sites {
				current[site.NameHierarchy] = site
			}
			for _, position := range positions {
				floor := current[position.floor]
				if floor.ID != floorIDs[position.floor] {
					return errConfigurationStale
				}
				geometry, err := plannedAPObservedGeometry(floor)
				if err != nil {
					return err
				}
				if err := validatePlannedAPPosition(position.payload, geometry); err != nil {
					return err
				}
			}
			fresh, err := e.observe(ctx)
			if err != nil {
				return err
			}
			view, err := plannedAPDispatchView(fresh)
			if err != nil {
				return err
			}
			if !bytes.Equal(view, frozen) {
				return errConfigurationStale
			}
			if e.dispatchGuard != nil {
				if err := e.dispatchGuard(ctx, write.resource, write.action, write.identity); err != nil {
					return err
				}
				fresh, err = e.observe(ctx)
				if err != nil {
					return err
				}
				view, err = plannedAPDispatchView(fresh)
				if err != nil {
					return err
				}
				if !bytes.Equal(view, frozen) {
					return errConfigurationStale
				}
			}
			return nil
		})
		if first == nil {
			first = write
		}
	}
	return first, nil
}
func (e *plannedAPExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || !validAPIID(op.ParentID) || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	observations, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, o := range observations {
		if !plannedAPResourceMatches(op.Resource, o.want) {
			continue
		}
		if o.floorID != op.ParentID || (o.actual.ID != "" && op.TargetID != "" && o.actual.ID != op.TargetID) {
			return false, errConfigurationStale
		}
		if o.actual.ID == "" {
			return false, nil
		}
		body, changed, err := plannedAPUpdatePayload(o.want, o.actual)
		if err != nil {
			return false, err
		}
		geometry, err := plannedAPObservedGeometry(o.floor)
		if err != nil {
			return false, err
		}
		if err := validatePlannedAPPosition(body, geometry); err != nil {
			return false, err
		}
		return !changed, nil
	}
	return false, errConfigurationTransition
}

// Explicit tuples freeze private typed observations; unexported state fields
// must not be marshaled directly (that would produce empty objects).
func plannedAPDispatchView(observations []plannedAPState) ([]byte, error) {
	rows := make([]any, 0, len(observations))
	for _, state := range observations {
		rows = append(rows, []any{state.want.floor, state.want.payload, state.floorID, state.floor, state.actual, state.native})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return nil, errInvalidResponse
	}
	return raw, nil
}
