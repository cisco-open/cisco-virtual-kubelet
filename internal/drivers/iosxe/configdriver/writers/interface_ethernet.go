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

func init() {
	registerSkeleton("interface_ethernet",
		"/Cisco-IOS-XE-native:native/interface/GigabitEthernet",
		"/Cisco-IOS-XE-native:native/interface/TwoGigabitEthernet",
		"/Cisco-IOS-XE-native:native/interface/FiveGigabitEthernet",
		"/Cisco-IOS-XE-native:native/interface/TenGigabitEthernet",
		"/Cisco-IOS-XE-native:native/interface/TwentyFiveGigE",
		"/Cisco-IOS-XE-native:native/interface/FortyGigabitEthernet",
		"/Cisco-IOS-XE-native:native/interface/HundredGigE",
		"/Cisco-IOS-XE-native:native/interface/TwoHundredGigE",
		"/Cisco-IOS-XE-native:native/interface/FourHundredGigE",
	)
}
