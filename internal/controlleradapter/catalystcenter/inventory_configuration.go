// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "errors"

// Inventory and templates use the same tag namespace. Device actions must not
// be discarded when a caller requests the currently supported tag-only apply.
func parseInventoryTags(raw []byte) ([]desiredTag, error) {
	intent, err := parseInventoryIntent(raw)
	if err != nil {
		return nil, err
	}
	if len(intent.devices) > 0 || len(intent.discoveries) > 0 {
		return nil, errors.New("tag-only projection cannot discard device or discovery intent")
	}
	return intent.tags, nil
}
