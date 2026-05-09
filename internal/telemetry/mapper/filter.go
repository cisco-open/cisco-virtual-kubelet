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
	"regexp"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// Filter applies the two mapper filter stages. WirePathFilter is evaluated
// before alias resolution; MetricNameFilter is evaluated after aliasing.
type Filter struct {
	wirePath   ruleSet
	metricName ruleSet
}

func NewFilter(cfg *configv1alpha1.FilterConfig) Filter {
	if cfg == nil {
		return Filter{}
	}
	return Filter{
		wirePath:   newRuleSet(cfg.WirePath),
		metricName: newRuleSet(cfg.MetricName),
	}
}

func (f Filter) AllowWirePath(path string) bool {
	return f.wirePath.allow(path)
}

func (f Filter) AllowMetricName(name string) bool {
	return f.metricName.allow(name)
}

type ruleSet struct {
	allowRules []globRule
	denyRules  []globRule
}

func newRuleSet(rules *configv1alpha1.FilterRules) ruleSet {
	if rules == nil {
		return ruleSet{}
	}
	return ruleSet{
		allowRules: compileGlobRules(rules.Allow),
		denyRules:  compileGlobRules(rules.Deny),
	}
}

func (r ruleSet) allow(value string) bool {
	if len(r.allowRules) > 0 && !matchAny(r.allowRules, value) {
		return false
	}
	return !matchAny(r.denyRules, value)
}

type globRule struct {
	re *regexp.Regexp
}

func compileGlobRules(patterns []string) []globRule {
	out := make([]globRule, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		out = append(out, globRule{re: regexp.MustCompile(globToRegexp(pattern))})
	}
	return out
}

func matchAny(rules []globRule, value string) bool {
	for _, rule := range rules {
		if rule.re.MatchString(value) {
			return true
		}
	}
	return false
}

func globToRegexp(pattern string) string {
	out := "^"
	for _, r := range pattern {
		switch r {
		case '*':
			out += ".*"
		case '?':
			out += "."
		default:
			out += regexp.QuoteMeta(string(r))
		}
	}
	return out + "$"
}
