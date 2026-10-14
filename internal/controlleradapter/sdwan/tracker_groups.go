// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

var parcelReferenceID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func compileTrackerGroups(p FeatureProfile, collection, key string) ([]Resource, error) {
	out := []Resource{}
	for _, family := range []struct {
		public, kind, path, target string
		groups, trackers           []map[string]any
	}{
		{"ipv4_tracker_groups", collection + "_tracker_group", "trackergroup", "tracker", p.IPv4TrackerGroups, p.IPv4Trackers},
		{"ipv6_tracker_groups", "transport_ipv6_tracker_group", "ipv6-trackergroup", "ipv6-tracker", p.IPv6TrackerGroups, p.IPv6Trackers},
		{"object_tracker_groups", "service_object_tracker_group", "objecttrackergroup", "objecttracker", p.ObjectTrackerGroups, p.ObjectTrackers},
	} {
		declared := map[string]bool{}
		for _, tracker := range family.trackers {
			name, _ := tracker["name"].(string)
			declared[name] = name != ""
		}
		seen := map[string]bool{}
		for _, input := range family.groups {
			refs, ok := input["trackers"].([]any)
			if !ok || len(refs) != 2 {
				return nil, errors.New("tracker group requires two declared tracker names")
			}
			copy := map[string]any{}
			for k, v := range input {
				copy[k] = v
			}
			ids := []any{}
			references := []ParcelReference{}
			unique := map[string]bool{}
			for i, value := range refs {
				name, ok := value.(string)
				if !ok || !declared[name] || unique[name] {
					return nil, errors.New("tracker references must be distinct names declared in this profile")
				}
				unique[name] = true
				// Schema-valid placeholders are replaced from controller observations
				// before every write; public intent never contains controller IDs.
				ids = append(ids, parcelPlaceholder(name))
				references = append(references, ParcelReference{Name: name, ParcelPath: family.target, BodyPath: []string{"data", "trackerRefs", strconv.Itoa(i), "trackerRef", "refId", "value"}})
			}
			copy["trackers"] = ids
			meta, data, err := scalarParcelData(family.kind, copy)
			if err != nil {
				return nil, err
			}
			if meta.Name == nil || *meta.Name == "" || len(*meta.Name) > 128 || seen[*meta.Name] {
				return nil, errors.New("tracker groups require unique names")
			}
			seen[*meta.Name] = true
			out = append(out, Resource{Key: key + "/" + family.public + "/" + *meta.Name, Name: *meta.Name, Section: "feature_profiles", Kind: "parcel", Type: family.kind, Collection: "/dataservice/v1/feature-profile/sdwan/" + collection, ParentName: p.Name, ParcelPath: family.path, ParcelRefs: references, Body: map[string]any{"name": *meta.Name, "description": meta.Description, "data": data}})
		}
	}
	return out, nil
}

func (c *Client) resolveParcelRefs(ctx context.Context, r Resource) error {
	for _, reference := range r.ParcelRefs {
		target := Resource{Kind: "parcel", Name: reference.Name, Collection: r.Collection, ParentName: r.ParentName, ParcelPath: reference.ParcelPath}
		observed, err := c.observeModern(ctx, target)
		if err != nil {
			return err
		}
		if observed == nil {
			return ErrDependencyPending
		}
		if !parcelReferenceID.MatchString(observed.ID) {
			return ErrResponse
		}
		if err := replaceParcelReference(r.Body, reference.BodyPath, observed.ID); err != nil {
			return err
		}
	}
	return nil
}

func replaceParcelReference(body any, path []string, id string) error {
	if len(path) == 0 || !validID.MatchString(id) {
		return ErrResponse
	}
	switch node := body.(type) {
	case map[string]any:
		value, exists := node[path[0]]
		if !exists {
			return ErrResponse
		}
		if len(path) == 1 {
			node[path[0]] = id
			return nil
		}
		return replaceParcelReference(value, path[1:], id)
	case []any:
		i, err := strconv.Atoi(path[0])
		if err != nil || i < 0 || i >= len(node) || len(path) == 1 {
			return ErrResponse
		}
		return replaceParcelReference(node[i], path[1:], id)
	default:
		return ErrResponse
	}
}

func parcelPlaceholder(name string) string {
	hash := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%x-%x-%x-%x-%x", hash[0:4], hash[4:6], hash[6:8], hash[8:10], hash[10:16])
}
