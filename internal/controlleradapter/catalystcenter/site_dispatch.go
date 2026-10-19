// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
)

// Freeze private intent as bytes rather than hashing or publishing site values.
// desiredSite's fields are private, so marshaling the struct directly loses them.
func siteDispatchIntent(desired []desiredSite) ([]byte, error) {
	rows := make([]any, 0, len(desired))
	for _, d := range desired {
		rows = append(rows, []any{d.kind, d.path, d.fields})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return nil, errInvalidResponse
	}
	return raw, nil
}

// Protect the typed native target and ancestor projection, including fields
// preserved by PUT. Unrelated sites and collection row order are not authority
// for this singular write. This observation is not creator/deletion ownership.
func siteDispatchFootprint(actual []Site, path string) ([]byte, error) {
	if !validSitePath(path) || path == "Global" {
		return nil, errInvalidResponse
	}
	byPath, ids := map[string]Site{}, map[string]bool{}
	for _, s := range actual {
		if !validAPIID(s.ID) || !validSitePath(s.NameHierarchy) || ids[s.ID] {
			return nil, errInvalidResponse
		}
		if _, exists := byPath[s.NameHierarchy]; exists {
			return nil, errInvalidResponse
		}
		byPath[s.NameHierarchy], ids[s.ID] = s, true
	}
	target, present := byPath[path]
	if present && target.Name != lastSiteName(path) {
		return nil, errInvalidResponse
	}
	rows := []Site{}
	if present {
		rows = append(rows, target)
	}
	for ancestor := parentSitePath(path); ; ancestor = parentSitePath(ancestor) {
		s, exists := byPath[ancestor]
		if !exists || s.Name != lastSiteName(ancestor) || ancestor == "Global" && s.Type != "global" {
			return nil, errInvalidResponse
		}
		if len(rows) > 0 {
			child := rows[len(rows)-1]
			if child.ParentID != s.ID || !validSiteParent(child.Type, s.Type) {
				return nil, errInvalidResponse
			}
		}
		rows = append(rows, s)
		if ancestor == "Global" {
			break
		}
	}
	raw, err := json.Marshal(struct {
		Path    string
		Present bool
		Sites   []Site
	}{path, present, rows})
	if err != nil {
		return nil, errInvalidResponse
	}
	return raw, nil
}

func siteMutationDispatchView(m *siteMutation) ([]byte, error) {
	if m == nil {
		return nil, errConfigurationStale
	}
	raw, err := json.Marshal([]any{m.desired.kind, m.desired.path, m.desired.fields, m.id, m.parentID, m.action, m.method, m.endpoint, json.RawMessage(m.body)})
	if err != nil {
		return nil, errInvalidResponse
	}
	return raw, nil
}

func (e *siteConfigurationExecutor) singularWrite(ctx context.Context, actual []Site, m *siteMutation) (*configurationWrite, error) {
	intent, err := siteDispatchIntent(e.desired)
	if err != nil {
		return nil, err
	}
	footprint, err := siteDispatchFootprint(actual, m.desired.path)
	if err != nil {
		return nil, err
	}
	mutation, err := siteMutationDispatchView(m)
	if err != nil {
		return nil, err
	}
	write := &configurationWrite{resource: m.desired.path, action: m.action, identity: configurationResourceIdentity{target: m.id, parent: m.parentID}, submit: func(ctx context.Context) (Task, error) { return e.client.submitSiteMutation(ctx, m) }}
	privateView := ""
	if e.comparison != nil {
		if e.privateResource == nil {
			return nil, errConfigurationTransition
		}
		if m.action == "Create" {
			write.resource = e.privateResource(m.desired)
		} else if m.action == "Update" {
			write.resource, err = e.privateUpdateResource(m.desired)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, errConfigurationTransition
		}
		if err := e.comparison.check(ctx); err != nil {
			return nil, err
		}
		view, err := e.client.siteNativeFootprint(ctx, actual, m.desired.path, true)
		if err != nil {
			return nil, err
		}
		privateView, err = siteNativeMAC(e.comparison, write.resource, view)
		if err != nil {
			return nil, err
		}
	}
	observe := func(ctx context.Context) error {
		currentIntent, err := siteDispatchIntent(e.desired)
		if err != nil || !bytes.Equal(currentIntent, intent) {
			return errConfigurationStale
		}
		current, err := e.client.ListSites(ctx)
		if err != nil {
			return err
		}
		view, err := siteDispatchFootprint(current, m.desired.path)
		if err != nil {
			return err
		}
		if !bytes.Equal(view, footprint) {
			return errConfigurationStale
		}
		planned, err := nextSiteMutation(e.desired, current)
		if err != nil {
			return err
		}
		view, err = siteMutationDispatchView(planned)
		if err != nil || !bytes.Equal(view, mutation) {
			return errConfigurationStale
		}
		if e.comparison != nil {
			if err := e.comparison.check(ctx); err != nil {
				return err
			}
			view, err := e.client.siteNativeFootprint(ctx, current, m.desired.path, true)
			if err != nil {
				return err
			}
			mac, err := siteNativeMAC(e.comparison, write.resource, view)
			if err != nil {
				return err
			}
			if !hmac.Equal([]byte(mac), []byte(privateView)) {
				return errConfigurationStale
			}
			if err := e.comparison.check(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		if err := observe(ctx); err != nil {
			return err
		}
		if e.submissionGuard != nil {
			if err := e.submissionGuard(ctx, write.resource, write.action, write.identity); err != nil {
				return err
			}
		}
		return observe(ctx)
	})
	return write, nil
}
