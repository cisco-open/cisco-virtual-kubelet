// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"strings"
)

const wirelessPolicyCohortPrefix = "policy-tag-cohort:v1:"

var errWirelessPolicyCohortRequired = errors.New("creating a shared-site policy tag requires an intact new cohort")

func wirelessPolicyCohortResource(rows []wirelessProfileTagObservation) (string, []byte, error) {
	if len(rows) < 2 {
		return "", nil, errConfigurationTransition
	}
	profile := rows[0].profileID
	members := []string{}
	items := []map[string]any{}
	for _, row := range rows {
		if !validAPIID(profile) || row.profileID != profile {
			return "", nil, errConfigurationTransition
		}
		members = append(members, row.resource)
		items = append(items, row.payload)
	}
	body, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		return "", nil, err
	}
	binding, err := json.Marshal([]any{profile, members, items})
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("%s%x", wirelessPolicyCohortPrefix, sha256.Sum256(binding)), body, nil
}

func policyRowsForProfile(rows []wirelessProfileTagObservation, profile string) []wirelessProfileTagObservation {
	out := []wirelessProfileTagObservation{}
	for _, row := range rows {
		if row.profileID == profile {
			out = append(out, row)
		}
	}
	return out
}

// This is one ordinary metadata operation in the existing journal. All declared
// tags for this profile must be new: mixed existing/missing cohorts stay fenced.
// Native single-item creates can replace another disjoint tag at the same site.
func (e *wirelessProfileTagExecutor) planPolicyCohort(rows []wirelessProfileTagObservation) (*configurationWrite, error) {
	if !e.policy {
		return nil, nil
	}
	for _, row := range rows {
		if row.actual != nil {
			continue
		}
		cohort := policyRowsForProfile(rows, row.profileID)
		if len(cohort) < 2 {
			continue
		}
		for _, member := range cohort {
			if member.actual != nil {
				return nil, errWirelessPolicyCohortRequired
			}
		}
		resource, body, err := wirelessPolicyCohortResource(cohort)
		if err != nil {
			return nil, err
		}
		profile := row.profileID
		write := &configurationWrite{resource: resource, action: "Create", identity: configurationResourceIdentity{parent: profile}, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, http.MethodPost, wirelessProfileTagPath(profile, true)+"/bulk", body)
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
		}}
		intent, err := json.Marshal(wirelessProfileDispatchIntent(e.desired))
		if err != nil {
			return nil, err
		}
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			currentIntent, err := json.Marshal(wirelessProfileDispatchIntent(e.desired))
			if err != nil || !bytes.Equal(intent, currentIntent) {
				return errConfigurationStale
			}
			fresh, err := e.observe(ctx)
			if err != nil {
				return err
			}
			current := policyRowsForProfile(fresh, profile)
			for _, member := range current {
				if member.actual != nil {
					return errConfigurationStale
				}
			}
			identity, payload, err := wirelessPolicyCohortResource(current)
			if err != nil || identity != resource || !reflect.DeepEqual(payload, body) {
				return errConfigurationStale
			}
			return nil
		})
		return write, nil
	}
	return nil, nil
}

func (e *wirelessProfileTagExecutor) verifyPolicyCohort(rows []wirelessProfileTagObservation, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if !e.policy || op.Action != "Create" || op.TargetID != "" || !strings.HasPrefix(op.Resource, wirelessPolicyCohortPrefix) {
		return false, errConfigurationTransition
	}
	cohort := policyRowsForProfile(rows, op.ParentID)
	resource, _, err := wirelessPolicyCohortResource(cohort)
	if err != nil || resource != op.Resource {
		return false, errConfigurationStale
	}
	for _, member := range cohort {
		if member.actual == nil || !reflect.DeepEqual(member.payload, member.actual.fields) {
			return false, nil
		}
	}
	return true, nil
}
