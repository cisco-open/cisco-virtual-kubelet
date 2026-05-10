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

var forbiddenDataPointAttrs = map[string]struct{}{
	"service.name":         {},
	"service_name":         {},
	"service.instance.id":  {},
	"service_instance_id":  {},
	"cvk.process.role":     {},
	"cvk_process_role":     {},
	"cvk.driver.kind":      {},
	"cvk_driver_kind":      {},
	"cisco.device.name":    {},
	"cisco_device_name":    {},
	"cisco.device.address": {},
	"cisco_device_address": {},
	"cluster":              {},
	"env":                  {},
	"owner":                {},
	"host.name":            {},
	"host_name":            {},
	"net.peer.name":        {},
	"net_peer_name":        {},
	"k8s.pod.name":         {},
	"k8s_pod_name":         {},
	"k8s.namespace.name":   {},
	"k8s_namespace_name":   {},
	"k8s.node.name":        {},
	"k8s_node_name":        {},
	"k8s.pod.uid":          {},
	"k8s_pod_uid":          {},
}

func IsForbiddenDataPointAttribute(key string) bool {
	_, ok := forbiddenDataPointAttrs[key]
	return ok
}

func stripForbiddenDataPointAttributes(attrs []KeyValue) []KeyValue {
	out := attrs[:0]
	for _, attr := range attrs {
		if IsForbiddenDataPointAttribute(attr.Key) {
			continue
		}
		out = append(out, attr)
	}
	return out
}
