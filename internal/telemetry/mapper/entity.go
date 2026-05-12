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

package mapper

import (
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/semconv"
)

func entityAttributes(ctx EventContext, canonicalPath string, tuple ListKeyTuple) []KeyValue {
	entityType, entityID := entityIdentity(ctx, canonicalPath, tuple)
	return []KeyValue{
		{Key: semconv.CvkEntityType, Value: entityType},
		{Key: semconv.CvkEntityID, Value: entityID},
	}
}

func entityIdentity(ctx EventContext, canonicalPath string, tuple ListKeyTuple) (string, string) {
	keyValues := tupleKeyValues(tuple)
	if isPodEntity(canonicalPath, tuple) {
		if id := firstKeyValue(keyValues, "uid", "pod_uid", "pod-uid", "k8s_pod_uid", "k8s.pod.uid"); id != "" {
			return semconv.EntityTypePod, id
		}
		if name := firstKeyValue(keyValues, "pod", "pod_name", "pod-name", "name"); name != "" {
			return semconv.EntityTypePod, name
		}
	}
	if isAppEntity(canonicalPath, tuple) {
		if id := firstKeyValue(keyValues, "uid", "pod_uid", "pod-uid", "k8s_pod_uid", "k8s.pod.uid"); id != "" {
			return semconv.EntityTypeApp, id
		}
		if id := firstKeyValue(keyValues, "app_id", "app-id", "application_id", "application-id", "name"); id != "" {
			if uid := uidFromAppID(id); uid != "" {
				return semconv.EntityTypeApp, uid
			}
			return semconv.EntityTypeApp, id
		}
	}
	if isInterfaceEntity(tuple) {
		name := firstKeyValue(keyValues, "name", "interface", "interface_name", "interface-name")
		ifIndex := firstKeyValue(keyValues, "if_index", "if-index", "ifindex", "index")
		switch {
		case name != "" && ifIndex != "":
			return semconv.EntityTypeInterface, name + ":" + ifIndex
		case name != "":
			return semconv.EntityTypeInterface, name
		case ifIndex != "":
			return semconv.EntityTypeInterface, ifIndex
		}
	}
	return semconv.EntityTypeDevice, deviceEntityID(ctx)
}

func tupleKeyValues(tuple ListKeyTuple) map[string]string {
	out := make(map[string]string, len(tuple)*2)
	for _, key := range tuple {
		if key.KeyValue == "" {
			continue
		}
		out[key.KeyName] = key.KeyValue
		out[normalizeEntityKey(key.KeyName)] = key.KeyValue
		list := normalizeEntityKey(listName(key.ListPath))
		if list != "" {
			out[list+"_"+normalizeEntityKey(key.KeyName)] = key.KeyValue
		}
	}
	return out
}

func firstKeyValue(values map[string]string, aliases ...string) string {
	for _, alias := range aliases {
		for _, key := range []string{alias, normalizeEntityKey(alias)} {
			if value := strings.TrimSpace(values[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func isPodEntity(canonicalPath string, tuple ListKeyTuple) bool {
	rawPath := strings.ToLower(canonicalPath)
	path := normalizeEntityKey(canonicalPath)
	if strings.Contains(rawPath, "/pods/pod") || strings.Contains(rawPath, "/pod[") ||
		strings.Contains(path, "pods_pod") {
		return true
	}
	for _, key := range tuple {
		switch normalizeEntityKey(listName(key.ListPath)) {
		case "pod", "pods":
			return true
		}
	}
	return false
}

func isAppEntity(canonicalPath string, tuple ListKeyTuple) bool {
	rawPath := strings.ToLower(canonicalPath)
	path := normalizeEntityKey(canonicalPath)
	if strings.Contains(path, "app_hosting") || strings.Contains(rawPath, "/app[") || strings.Contains(rawPath, "/application[") {
		return true
	}
	for _, key := range tuple {
		switch normalizeEntityKey(listName(key.ListPath)) {
		case "app", "application":
			return true
		}
	}
	return false
}

func isInterfaceEntity(tuple ListKeyTuple) bool {
	for _, key := range tuple {
		if normalizeEntityKey(listName(key.ListPath)) == "interface" {
			return true
		}
	}
	return false
}

func uidFromAppID(appID string) string {
	if _, suffix, ok := strings.Cut(appID, "_"); ok && suffix != "" {
		return suffix
	}
	return ""
}

func deviceEntityID(ctx EventContext) string {
	for _, key := range []string{
		"cisco.device.serial_number",
		"cisco.device.serial.number",
		"device.serial.number",
		"device.serial_number",
		"router.serial",
		"serial_number",
		"serial-number",
		"device.uuid",
		"uuid",
	} {
		if value := strings.TrimSpace(ctx.ResourceAttributes[key]); value != "" {
			return value
		}
	}
	if ctx.Device != "" {
		return ctx.Device
	}
	if ctx.Subscription != "" {
		return ctx.Subscription
	}
	return "unknown"
}

func normalizeEntityKey(in string) string {
	in = strings.ToLower(strings.TrimSpace(in))
	replacer := strings.NewReplacer("-", "_", ".", "_", "/", "_", "[", "_", "]", "_", "=", "_")
	return strings.Trim(replacer.Replace(in), "_")
}
