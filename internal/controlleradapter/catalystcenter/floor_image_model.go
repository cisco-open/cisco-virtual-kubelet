// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type floorImageIntent struct {
	floor, filename, digest string
	content                 []byte
}

func (floorImageIntent) String() string   { return "<floor image snapshot>" }
func (floorImageIntent) GoString() string { return "<floor image snapshot>" }
func (i floorImageIntent) resource() string {
	return fmt.Sprintf("floor-image:%x", sha256.Sum256([]byte(i.floor)))
}

// Resolve image_path only against the source's explicit asset snapshot. The
// public path stays a NetAsCode model field; it grants no filesystem or URL read.
// Strip only this independent resource before reusing geometry/AP validation.
func parseSitesWithFloorImages(raw []byte, assets map[string][]byte) ([]desiredSite, []plannedAPIntent, []floorImageIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, nil, nil, errFloorImage
	}
	sites, ok := root["sites"].(map[string]any)
	if !ok {
		return nil, nil, nil, errFloorImage
	}
	images := []floorImageIntent{}
	if value, present := sites["floors"]; present {
		floors, ok := value.([]any)
		if !ok {
			return nil, nil, nil, errFloorImage
		}
		for _, value := range floors {
			floor, ok := value.(map[string]any)
			if !ok {
				return nil, nil, nil, errFloorImage
			}
			value, present := floor["image_path"]
			if !present {
				continue
			}
			delete(floor, "image_path")
			// The pinned module ignores a null image_path; it does not remove an image.
			if value == nil {
				continue
			}
			filename, ok := value.(string)
			if !ok || filename == "" {
				return nil, nil, nil, errFloorImage
			}
			name, nok := floor["name"].(string)
			parent, pok := floor["parent_name"].(string)
			content, exists := assets[filename]
			if !nok || !pok || !exists {
				return nil, nil, nil, errFloorImage
			}
			if _, err := validateFloorImage(filename, content); err != nil {
				return nil, nil, nil, err
			}
			images = append(images, floorImageIntent{floor: parent + "/" + name, filename: filename, digest: fmt.Sprintf("%x", sha256.Sum256(content)), content: bytes.Clone(content)})
		}
	}
	geometry, err := json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return nil, nil, nil, errFloorImage
	}
	desired, placements, err := parseSitesWithPlannedAPs(geometry)
	if err != nil {
		return nil, nil, nil, err
	}
	sort.Slice(images, func(i, j int) bool { return images[i].floor < images[j].floor })
	return desired, placements, images, nil
}
