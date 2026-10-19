// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Index logical NetAsCode image_path values to BinaryData keys on the SAME
// ConfigMap as the model. No paths are opened or URLs followed. A single UID/RV
// binds the model, asset index and bytes without introducing another reader.
const floorImageAssetsKey = "cvk-floor-image-assets.json"

func loadFloorImageAssets(cm *corev1.ConfigMap, modelKey string) (map[string][]byte, error) {
	if cm == nil || modelKey == floorImageAssetsKey {
		return nil, errFloorImage
	}
	if _, present := cm.BinaryData[floorImageAssetsKey]; present {
		return nil, errFloorImage
	}
	raw, present := cm.Data[floorImageAssetsKey]
	if !present {
		return nil, nil
	}
	if len(raw) > 1<<20 {
		return nil, errFloorImage
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errFloorImage
	}
	assets := map[string][]byte{}
	total := 0
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || len(assets) >= 64 {
			return nil, errFloorImage
		}
		if _, duplicate := assets[name]; duplicate {
			return nil, errFloorImage
		}
		var key string
		if decoder.Decode(&key) != nil || len(validation.IsConfigMapKey(key)) != 0 || key == modelKey || key == floorImageAssetsKey {
			return nil, errFloorImage
		}
		image, present := cm.BinaryData[key]
		if _, text := cm.Data[key]; !present || text {
			return nil, errFloorImage
		}
		if _, err := validateFloorImage(name, image); err != nil {
			return nil, err
		}
		total += len(image)
		if total > maxFloorImageBytes {
			return nil, errFloorImage
		}
		assets[name] = bytes.Clone(image)
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, errFloorImage
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errFloorImage
	}
	return assets, nil
}
