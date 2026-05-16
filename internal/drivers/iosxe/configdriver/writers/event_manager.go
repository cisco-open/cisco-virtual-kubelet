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

package writers

// Embedded Event Manager (EEM) Phase-3 writer. EEM applets are
// site-specific and often include embedded scripts; Phase-3
// manages the container as a singleton with the common leaves.
//
// IOS-XE 17.16 structural differences (handled via BodyTransform):
//   - event.timer  → event.Cisco-IOS-XE-eem:timer-choice
//   - event.syslog → event.Cisco-IOS-XE-eem:syslog-choice
//   - event.track  → event.Cisco-IOS-XE-eem:track-choice
//   - event.none   → event.Cisco-IOS-XE-eem:none-choice
//   - applet.action[] → applet.Cisco-IOS-XE-eem:action-config.Cisco-IOS-XE-eem:action[]
//   - action[].cli    → Cisco-IOS-XE-eem:cli-choice
//   - action[].syslog → Cisco-IOS-XE-eem:syslog-option
// FetchBodyTransform reverses the above for Diff comparison.

// eemBodyTransform1716 converts the 17.18 EEM YANG shape to the
// 17.16 shape, adding all required Cisco-IOS-XE-eem: module prefixes.
func eemBodyTransform1716(body map[string]any) map[string]any {
	applets, ok := body["applet"].([]any)
	if !ok {
		return body
	}
	for i, a := range applets {
		applet, ok := a.(map[string]any)
		if !ok {
			continue
		}
		if event, ok := applet["event"].(map[string]any); ok {
			applet["event"] = eemTransformEvent1716(event)
		}
		if actions, ok := applet["action"].([]any); ok {
			converted := make([]any, 0, len(actions))
			for _, act := range actions {
				entry, ok := act.(map[string]any)
				if !ok {
					converted = append(converted, act)
					continue
				}
				converted = append(converted, eemTransformAction1716(entry))
			}
			applet["Cisco-IOS-XE-eem:action-config"] = map[string]any{
				"Cisco-IOS-XE-eem:action": converted,
			}
			delete(applet, "action")
		}
		applets[i] = applet
	}
	body["applet"] = applets
	return body
}

func eemTransformEvent1716(event map[string]any) map[string]any {
	out := make(map[string]any, len(event))
	for k, v := range event {
		switch k {
		case "timer":
			out["Cisco-IOS-XE-eem:timer-choice"] = eemPrefixTimerMap1716(v)
		case "syslog":
			out["Cisco-IOS-XE-eem:syslog-choice"] = v
		case "track":
			out["Cisco-IOS-XE-eem:track-choice"] = v
		case "none":
			out["Cisco-IOS-XE-eem:none-choice"] = v
		default:
			out[k] = v
		}
	}
	return out
}

// eemPrefixTimerMap1716 adds Cisco-IOS-XE-eem: prefix to timer
// sub-container keys (cron, absolute, watchdog, countdown).
func eemPrefixTimerMap1716(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	subKeys := map[string]string{
		"cron": "Cisco-IOS-XE-eem:cron", "absolute": "Cisco-IOS-XE-eem:absolute",
		"watchdog": "Cisco-IOS-XE-eem:watchdog", "countdown": "Cisco-IOS-XE-eem:countdown",
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if newK, ok := subKeys[k]; ok {
			out[newK] = v
		} else {
			out[k] = v
		}
	}
	return out
}

func eemTransformAction1716(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		switch k {
		case "cli":
			out["Cisco-IOS-XE-eem:cli-choice"] = v
		case "syslog":
			out["Cisco-IOS-XE-eem:syslog-option"] = v
		default:
			out[k] = v
		}
	}
	return out
}

// eemFetchTransform1716 reverses eemBodyTransform1716 on the Fetch
// path, converting observed 17.16 YANG shapes back to 17.18 canonical
// netascode shapes for Diff comparison.
func eemFetchTransform1716(body map[string]any) map[string]any {
	applets, ok := body["applet"].([]any)
	if !ok {
		return body
	}
	for i, a := range applets {
		applet, ok := a.(map[string]any)
		if !ok {
			continue
		}
		if event, ok := applet["event"].(map[string]any); ok {
			applet["event"] = eemReverseEvent1716(event)
		}
		// Remove the 17.18-style action[] view the device returns
		// alongside action-config; use action-config as the source.
		delete(applet, "action")
		for _, acKey := range []string{"action-config", "Cisco-IOS-XE-eem:action-config"} {
			ac, ok := applet[acKey].(map[string]any)
			if !ok {
				continue
			}
			for _, actKey := range []string{"action", "Cisco-IOS-XE-eem:action"} {
				actions, ok := ac[actKey].([]any)
				if !ok {
					continue
				}
				converted := make([]any, 0, len(actions))
				for _, act := range actions {
					entry, ok := act.(map[string]any)
					if !ok {
						converted = append(converted, act)
						continue
					}
					converted = append(converted, eemReverseAction1716(entry))
				}
				applet["action"] = converted
				break
			}
			delete(applet, acKey)
			break
		}
		applets[i] = applet
	}
	body["applet"] = applets
	return body
}

func eemReverseEvent1716(event map[string]any) map[string]any {
	out := make(map[string]any, len(event))
	for k, v := range event {
		switch k {
		case "timer-choice", "Cisco-IOS-XE-eem:timer-choice":
			out["timer"] = eemUnprefixTimerMap1716(v)
		case "syslog-choice", "Cisco-IOS-XE-eem:syslog-choice":
			out["syslog"] = v
		case "track-choice", "Cisco-IOS-XE-eem:track-choice":
			out["track"] = v
		case "none-choice", "Cisco-IOS-XE-eem:none-choice":
			out["none"] = v
		default:
			out[k] = v
		}
	}
	return out
}

func eemUnprefixTimerMap1716(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	unprefix := map[string]string{
		"Cisco-IOS-XE-eem:cron": "cron", "Cisco-IOS-XE-eem:absolute": "absolute",
		"Cisco-IOS-XE-eem:watchdog": "watchdog", "Cisco-IOS-XE-eem:countdown": "countdown",
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if newK, ok := unprefix[k]; ok {
			out[newK] = v
		} else {
			out[k] = v
		}
	}
	return out
}

func eemReverseAction1716(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		switch k {
		case "cli-choice", "Cisco-IOS-XE-eem:cli-choice":
			out["cli"] = v
		case "syslog-option", "Cisco-IOS-XE-eem:syslog-option":
			out["syslog"] = v
		default:
			out[k] = v
		}
	}
	return out
}

func init() {
	Override(singletonWriter{
		family:      "event_manager",
		yangPath:    "/Cisco-IOS-XE-native:native/event/manager",
		envelopeKey: "Cisco-IOS-XE-eem:manager",
		managedLeaves: []string{
			"applet",
			"environment",
			"session",
		},
	})
}
