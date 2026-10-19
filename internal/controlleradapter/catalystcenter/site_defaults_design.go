// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Bind the reused parsing/asset/geometry policies, not only this wrapper.
//
//go:embed site_defaults_design.go
var siteDesignDefaultSource []byte

//go:embed floor_image_assets.go
var siteDesignAssetPolicy []byte

//go:embed floor_image_model.go
var siteDesignImagePolicy []byte

//go:embed floor_image_upload.go
var siteDesignImageValidationPolicy []byte

//go:embed planned_ap_model.go
var siteDesignAPPolicy []byte

//go:embed planned_ap_geometry.go
var siteDesignGeometryPolicy []byte

//go:embed nacmodel/document.go
var siteDesignDocumentPolicy []byte
var siteDesignDefaultPolicy = bytes.Join([][]byte{siteDesignDefaultSource, siteDesignAssetPolicy, siteDesignImagePolicy, siteDesignImageValidationPolicy, siteDesignAPPolicy, siteDesignGeometryPolicy, siteDesignDocumentPolicy}, []byte{0})

// ExportSiteDefaultsConfigMap consumes exactly one explicitly supplied offline
// snapshot. Its model, index and bytes use the runtime's same-ConfigMap rules;
// it performs no API request, native catalogue lookup or model-path file read.
func ExportSiteDefaultsConfigMap(raw []byte, modelKey string, overrides []byte, useBulkAPI bool) ([]byte, SiteDefaultsProvenance, error) {
	fail := func() ([]byte, SiteDefaultsProvenance, error) {
		return nil, SiteDefaultsProvenance{}, errors.New("unsupported site design ConfigMap snapshot")
	}
	cm, assets, err := decodeSiteDefaultSnapshot(raw, modelKey)
	if err != nil {
		return fail()
	}
	model := cm.Data[modelKey]
	out, meta, err := exportSiteDefaults([]byte(model), overrides, useBulkAPI, assets, true)
	if err != nil {
		return fail()
	}
	meta.SourceSnapshotSHA256 = networkExportDigest(raw)
	return out, meta, nil
}

func validateSiteExportPositions(sites []desiredSite, aps []plannedAPIntent) error {
	floors := map[string]desiredSite{}
	for _, site := range sites {
		if site.kind == "floor" {
			floors[site.path] = site
		}
	}
	for _, ap := range aps {
		floor, exists := floors[ap.floor]
		if !exists {
			return errPlannedAPModel
		}
		geometry, err := plannedAPDesiredGeometry(floor)
		if err != nil {
			return err
		}
		if err := validatePlannedAPPosition(ap.payload, geometry); err != nil {
			return err
		}
	}
	return nil
}

func decodeSiteDefaultSnapshot(raw []byte, modelKey string) (*corev1.ConfigMap, map[string][]byte, error) {
	if len(validation.IsConfigMapKey(modelKey)) != 0 {
		return nil, nil, errors.New("invalid site source ConfigMap")
	}
	node, err := nacmodel.ParseMappingDocument(raw)
	if err != nil {
		return nil, nil, errors.New("invalid site source ConfigMap")
	}
	var object map[string]any
	if node.Decode(&object) != nil {
		return nil, nil, errors.New("invalid site source ConfigMap")
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, nil, errors.New("invalid site source ConfigMap")
	}
	var cm corev1.ConfigMap
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cm) != nil || cm.Kind != "ConfigMap" || cm.APIVersion != "v1" {
		return nil, nil, errors.New("invalid site source ConfigMap")
	}
	_, present := cm.Data[modelKey]
	if !present {
		return nil, nil, errors.New("invalid site source ConfigMap")
	}
	if _, binary := cm.BinaryData[modelKey]; binary {
		return nil, nil, errors.New("invalid site source ConfigMap")
	}
	assets, err := loadFloorImageAssets(&cm, modelKey)
	if err != nil {
		return nil, nil, errors.New("invalid site source ConfigMap")
	}
	return &cm, assets, nil
}
