// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nexusdashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	fabricsPath = "/api/v1/manage/fabrics"
	// switchesPathFmt takes one validated fabric name.
	switchesPathFmt = "/api/v1/manage/fabric/%s/switches"

	// Paging query parameter names are UNVERIFIED against a live ND; the
	// swagger example only shows the meta.counts response envelope.
	pageSizeParam   = "max"
	pageOffsetParam = "offset"
	pageSize        = 100
	maxPages        = 500

	// PlatformNXOS marks switches CVK can adopt as NXOS devices later.
	PlatformNXOS  = "nxos"
	PlatformOther = "other"
)

// fabricNameRE bounds what is spliced into a URL path.
var fabricNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// nxosModelRE matches Nexus model strings such as N9K-C93180YC-FX3.
var nxosModelRE = regexp.MustCompile(`^N[0-9]+[A-Z]?-`)

// InventoryItem is the adapter-neutral view of one ND-managed switch.
// Role is empty until ND exposes it in the Manage switch schema.
type InventoryItem struct {
	Serial          string
	Hostname        string
	MgmtAddress     string
	Model           string
	Platform        string
	Fabric          string
	FabricType      string
	Role            string
	SoftwareVersion string
	Reachable       bool
	// SkipReason is set when the switch must not be adopted as a CVK node.
	SkipReason string
}

// ndSwitch is the subset of the ND Manage switch schema the adapter reads.
type ndSwitch struct {
	SwitchID           string `json:"switchId"`
	Hostname           string `json:"hostname"`
	Model              string `json:"model"`
	SoftwareVersion    string `json:"softwareVersion"`
	FabricName         string `json:"fabricName"`
	FabricType         string `json:"fabricType"`
	FabricManagementIP string `json:"fabricManagementIp"`
	Telemetry          struct {
		OutOfBandIPv4 string `json:"outOfBandIpV4Address"`
	} `json:"telemetryIpCollection"`
	Additional struct {
		DiscoveryStatus string `json:"discoveryStatus"`
		Vendor          string `json:"vendor"`
	} `json:"additionalSwitchData"`
}

type ndFabric struct {
	Name string `json:"name"`
}

type pageMeta struct {
	Meta struct {
		Counts struct {
			Remaining int `json:"remaining"`
			Total     int `json:"total"`
		} `json:"counts"`
	} `json:"meta"`
}

// listPaged follows meta.counts.remaining and returns the raw items found
// under key. It fails closed on a server that never drains, so a bad paging
// assumption cannot loop forever.
func (c *client) listPaged(ctx context.Context, path, key string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	offset := 0
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set(pageSizeParam, strconv.Itoa(pageSize))
		q.Set(pageOffsetParam, strconv.Itoa(offset))
		body, err := c.Get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		var env pageMeta
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("GET %s: response is not valid JSON", path)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("GET %s: response is not a JSON object", path)
		}
		var items []json.RawMessage
		if v, ok := raw[key]; ok && string(v) != "null" {
			if err := json.Unmarshal(v, &items); err != nil {
				return nil, fmt.Errorf("GET %s: %q is not a list", path, key)
			}
		}
		all = append(all, items...)
		if len(items) == 0 || env.Meta.Counts.Remaining <= 0 {
			return all, nil
		}
		offset += len(items)
	}
	return nil, fmt.Errorf("GET %s: paging did not terminate after %d pages", path, maxPages)
}

// ListInventory returns every switch in every fabric. Any fabric failure fails
// the whole call so callers keep their last complete snapshot.
func (c *client) ListInventory(ctx context.Context) ([]InventoryItem, error) {
	rawFabrics, err := c.listPaged(ctx, fabricsPath, "fabrics")
	if err != nil {
		return nil, fmt.Errorf("list fabrics: %w", sanitizeError(err))
	}
	var names []string
	for _, raw := range rawFabrics {
		var f ndFabric
		if err := json.Unmarshal(raw, &f); err != nil || !fabricNameRE.MatchString(f.Name) {
			continue // not addressable; never splice unvalidated text into a path
		}
		names = append(names, f.Name)
	}
	sort.Strings(names)
	var out []InventoryItem
	for _, name := range names {
		rawSwitches, err := c.listPaged(ctx, fmt.Sprintf(switchesPathFmt, name), "switches")
		if err != nil {
			return nil, fmt.Errorf("list switches in fabric %q: %w", name, sanitizeError(err))
		}
		for _, raw := range rawSwitches {
			var sw ndSwitch
			if err := json.Unmarshal(raw, &sw); err != nil {
				return nil, fmt.Errorf("decode switch in fabric %q: %w", name, err)
			}
			out = append(out, toInventoryItem(sw, name))
		}
	}
	return out, nil
}

func toInventoryItem(sw ndSwitch, fabric string) InventoryItem {
	item := InventoryItem{
		Serial:          sw.SwitchID,
		Hostname:        sw.Hostname,
		MgmtAddress:     firstNonEmpty(sw.FabricManagementIP, sw.Telemetry.OutOfBandIPv4),
		Model:           sw.Model,
		Fabric:          firstNonEmpty(sw.FabricName, fabric),
		FabricType:      sw.FabricType,
		SoftwareVersion: sw.SoftwareVersion,
		Reachable:       strings.EqualFold(sw.Additional.DiscoveryStatus, "ok"),
		Platform:        PlatformOther,
	}
	if nxosModelRE.MatchString(sw.Model) {
		item.Platform = PlatformNXOS
	}
	switch {
	case item.Serial == "":
		item.SkipReason = "missing serial number"
	case item.Platform != PlatformNXOS:
		item.SkipReason = fmt.Sprintf("platform %q is not NX-OS", firstNonEmpty(sw.Model, "unknown"))
	case item.MgmtAddress == "":
		item.SkipReason = "missing management address"
	}
	return item
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
