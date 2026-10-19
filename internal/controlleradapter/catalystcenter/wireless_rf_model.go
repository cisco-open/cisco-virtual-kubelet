// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const wirelessRFPath = "/dna/intent/api/v1/wirelessSettings/rfProfiles"

var errWirelessRFModel = errors.New("invalid RF profile model")

type wirelessRFIntent struct {
	name   string
	fields map[string]any
}

// Validate the entire object before projecting fields. Traversing only known
// leaves would silently discard typos or unsupported nested settings.
func validateWirelessRFKeys(row map[string]any, prefix string) error {
	if len(row) == 0 {
		return errWirelessRFModel
	}
	for key, value := range row {
		if strings.Contains(key, ".") || value == nil {
			return errWirelessRFModel
		}
		path := prefix + key
		if _, leaf := wirelessRFFields[path]; leaf {
			continue
		}
		known := false
		for field := range wirelessRFFields {
			if strings.HasPrefix(field, path+".") {
				known = true
				break
			}
		}
		child, ok := value.(map[string]any)
		if !known || !ok {
			return errWirelessRFModel
		}
		if err := validateWirelessRFKeys(child, path+"."); err != nil {
			return err
		}
	}
	return nil
}

func parseWirelessRFProfiles(raw []byte) ([]wirelessRFIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errWirelessRFModel
	}
	section, ok := root["wireless"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errWirelessRFModel
	}
	rows, ok := section["rf_profiles"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errWirelessRFModel
	}
	out := []wirelessRFIntent{}
	seen := map[string]bool{}
	defaults := 0
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok || validateWirelessRFKeys(row, "") != nil {
			return nil, errWirelessRFModel
		}
		name, ok := row["name"].(string)
		if !ok || !validProfileName(name) || seen[name] {
			return nil, errWirelessRFModel
		}
		seen[name] = true
		fields := map[string]any{"defaultRfProfile": false}
		for key, field := range wirelessRFFields {
			v, present := getWirelessAPField(row, key)
			if !present {
				continue
			}
			normalized, err := wirelessAPValue(field, v)
			if err != nil {
				return nil, errWirelessRFModel
			}
			normalized, err = normalizeWirelessRFList(field.path, normalized)
			if err != nil {
				return nil, err
			}
			if strings.HasSuffix(key, "_dbs_width") {
				n := normalized.(int64)
				if n != 20 && n != 40 && n != 80 && n != 160 && n != 320 {
					return nil, errWirelessRFModel
				}
			}
			setWirelessAPField(fields, field.path, normalized)
		}
		for _, required := range []string{"enableRadioTypeA", "enableRadioTypeB", "enableRadioType6GHz"} {
			if _, ok := fields[required]; !ok {
				return nil, errWirelessRFModel
			}
		}
		if fields["defaultRfProfile"] == true {
			defaults++
		}
		if err := validateWirelessRFRanges(fields); err != nil {
			return nil, err
		}
		out = append(out, wirelessRFIntent{name: name, fields: fields})
	}
	if defaults > 1 {
		return nil, errWirelessRFModel
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// Validate effective values as well as intent: a partial update can conflict
// with the retained bound from controller inventory.
func validateWirelessRFRanges(fields map[string]any) error {
	for _, band := range []string{"radioTypeAProperties", "radioTypeBProperties", "radioType6GHzProperties"} {
		for _, pair := range [][2]string{{"minPowerLevel", "maxPowerLevel"}, {"minDbsWidth", "maxDbsWidth"}, {"spatialReuseProperties.dot11axSrgObssPacketDetectMinThreshold", "spatialReuseProperties.dot11axSrgObssPacketDetectMaxThreshold"}} {
			lo, lok := getWirelessAPField(fields, band+"."+pair[0])
			hi, hik := getWirelessAPField(fields, band+"."+pair[1])
			if lok && hik && lo.(int64) > hi.(int64) {
				return errWirelessRFModel
			}
		}
		rates, hasRates := getWirelessAPField(fields, band+".dataRates")
		mandatory, hasMandatory := getWirelessAPField(fields, band+".mandatoryDataRates")
		if hasRates && hasMandatory {
			allowed := map[string]bool{}
			for _, rate := range strings.Split(rates.(string), ",") {
				allowed[rate] = true
			}
			for _, rate := range strings.Split(mandatory.(string), ",") {
				if !allowed[rate] {
					return errWirelessRFModel
				}
			}
		}
	}
	return nil
}

// These comma-separated API fields describe sets. Normalize both intent and
// inventory so controller ordering does not cause perpetual drift or replay.
func normalizeWirelessRFList(path string, value any) (any, error) {
	channels := strings.HasSuffix(path, ".radioChannels")
	if !channels && !strings.HasSuffix(path, ".dataRates") && !strings.HasSuffix(path, ".mandatoryDataRates") {
		return value, nil
	}
	s, ok := value.(string)
	if !ok {
		return nil, errWirelessRFModel
	}
	numbers := []float64{}
	seen := map[float64]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		// Restrict syntax to ordinary positive decimal values; reject exponents,
		// NaN and infinities rather than relying on the appliance parser.
		if part == "" || strings.Trim(part, "0123456789.") != "" {
			return nil, errWirelessRFModel
		}
		n, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || seen[n] || (channels && math.Trunc(n) != n) {
			return nil, errWirelessRFModel
		}
		seen[n] = true
		numbers = append(numbers, n)
	}
	sort.Float64s(numbers)
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = strconv.FormatFloat(n, 'f', -1, 64)
	}
	return strings.Join(parts, ","), nil
}
