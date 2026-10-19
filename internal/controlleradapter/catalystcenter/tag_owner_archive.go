// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const tagOwnerRetiredPrefix = "tag-owner-retired:v1:"
const tagOwnerRootPrefix = "tag-owner-root:v1:"
const tagSuccessorPrefix = "tag-successor:v1:"

func tagOwnerArchivedSelected(s string) bool {
	return strings.HasPrefix(s, tagOwnerRetiredPrefix) || strings.HasPrefix(s, tagOwnerRootPrefix)
}
func tagSuccessorSelected(s string) bool { return strings.HasPrefix(s, tagSuccessorPrefix) }
func tagSuccessorCanonical(s string) string {
	p := strings.Split(strings.TrimPrefix(s, tagSuccessorPrefix), ":")
	if !tagSuccessorSelected(s) || len(p) != 5 || p[0] != "c" || !validTemplateComparisonDigest(p[4]) {
		return ""
	}
	return tagOwnedPrefix + strings.Join(p[:4], ":")
}
func tagSuccessorSeal(s string) string {
	if tagSuccessorCanonical(s) == "" {
		return ""
	}
	return strings.Split(strings.TrimPrefix(s, tagSuccessorPrefix), ":")[4]
}

type tagOwnerFrame struct {
	Scope, UID                                   string
	ConfigGeneration, ControllerGeneration       int64
	SpecDigest, DeletionToken, PriorDigest, Seal string
}

func tagOwnerFrameText(f tagOwnerFrame) string {
	return strings.Join([]string{f.Scope, f.UID, strconv.FormatInt(f.ConfigGeneration, 36), strconv.FormatInt(f.ControllerGeneration, 36), f.SpecDigest, f.DeletionToken, f.PriorDigest, f.Seal}, ":")
}
func tagOwnerFrameParse(p []string) (tagOwnerFrame, bool) {
	if len(p) != 8 {
		return tagOwnerFrame{}, false
	}
	a, e := strconv.ParseInt(p[2], 36, 64)
	b, d := strconv.ParseInt(p[3], 36, 64)
	if e != nil || d != nil || a < 1 || b < 1 || strconv.FormatInt(a, 36) != p[2] || strconv.FormatInt(b, 36) != p[3] || p[1] == "" || strings.Contains(p[1], ":") || len(tagOwnerSections(p[0])) == 0 {
		return tagOwnerFrame{}, false
	}
	for _, v := range p[4:] {
		if !validTemplateComparisonDigest(v) {
			return tagOwnerFrame{}, false
		}
	}
	return tagOwnerFrame{p[0], p[1], a, b, p[4], p[5], p[6], p[7]}, true
}
func tagOwnerSections(scope string) []string {
	switch scope {
	case "i":
		return []string{"inventory"}
	case "t":
		return []string{"templates"}
	case "b":
		return []string{"inventory", "templates"}
	}
	return nil
}
func tagOwnerScope(sections []string) string {
	if combinedTagSections(sections) {
		return "b"
	}
	if len(sections) == 1 && sections[0] == "inventory" {
		return "i"
	}
	if len(sections) == 1 && sections[0] == "templates" {
		return "t"
	}
	return ""
}
func tagOwnerSeal(f tagOwnerFrame, own *cvk.NetworkControllerConfigurationStatus) string {
	f.Seal = ""
	return featureIdentityDigest(struct {
		Frame    tagOwnerFrame
		Evidence string
	}{f, featureIdentityDigest(own)})
}

type tagOwnerGroup struct {
	frame         tagOwnerFrame
	rows, wrapped []cvk.NetworkControllerConfigurationActionApplication
	carrier       int
}
type tagOwnerGraph struct {
	lastVerified time.Time
	rows         []cvk.NetworkControllerConfigurationActionApplication
	root         *tagOwnerFrame
	ids          map[string]bool
}

// Each owner keeps all original operations and result bytes. A short seal wraps
// every row; one already-short Delete result carries the complete frame within
// the existing 512-byte result bound. No foundation schema field is added.
func tagOwnerArchiveGraph(state *cvk.NetworkControllerConfigurationStatus) (tagOwnerGraph, error) {
	out := tagOwnerGraph{ids: map[string]bool{}}
	if state == nil {
		return out, nil
	}
	groups := map[string]*tagOwnerGroup{}
	seenOps := map[string]bool{}
	for _, r := range state.ActionApplications {
		selected := tagOwnedSelected(r.Operation.Resource) || tagDeleteSelected(r.Operation.Resource) || tagOwnerArchivedSelected(r.Resource) || tagRetiredSelected(r.Resource)
		if selected {
			if !configurationOperationID.MatchString(r.Operation.ID) || seenOps[r.Operation.ID] {
				return out, errConfigurationOwned
			}
			seenOps[r.Operation.ID] = true
		}
		if !tagOwnerArchivedSelected(r.Resource) {
			continue
		}
		seal, original := "", ""
		var frame tagOwnerFrame
		carrier := false
		if strings.HasPrefix(r.Resource, tagOwnerRootPrefix) {
			p := strings.SplitN(strings.TrimPrefix(r.Resource, tagOwnerRootPrefix), ":", 9)
			if len(p) != 9 {
				return out, errConfigurationOwned
			}
			var ok bool
			frame, ok = tagOwnerFrameParse(p[:8])
			if !ok {
				return out, errConfigurationOwned
			}
			seal, original, carrier = frame.Seal, p[8], true
		} else {
			p := strings.SplitN(strings.TrimPrefix(r.Resource, tagOwnerRetiredPrefix), ":", 2)
			if len(p) != 2 || !validTemplateComparisonDigest(p[0]) {
				return out, errConfigurationOwned
			}
			seal, original = p[0], p[1]
		}
		if tagOwnerArchivedSelected(original) || len(r.Resource) > 512 {
			return out, errConfigurationOwned
		}
		g := groups[seal]
		if g == nil {
			g = &tagOwnerGroup{}
			groups[seal] = g
		}
		if carrier {
			g.carrier++
			g.frame = frame
			if !validTagDeleteOperation(&r.Operation) {
				return out, errConfigurationOwned
			}
		}
		unwrapped := r
		unwrapped.Resource = original
		g.rows = append(g.rows, unwrapped)
		g.wrapped = append(g.wrapped, r)
	}
	seenOwners := map[string]bool{}
	visited := map[string]bool{}
	for len(visited) < len(groups) {
		prior := featureCohortDigest(out.rows)
		next := ""
		for seal, g := range groups {
			if visited[seal] {
				continue
			}
			if g.carrier != 1 {
				return out, errConfigurationOwned
			}
			if g.frame.PriorDigest == prior {
				if next != "" {
					return out, errConfigurationOwned
				}
				next = seal
			}
		}
		if next == "" {
			return out, errConfigurationOwned
		}
		g := groups[next]
		f := g.frame
		if seenOwners[f.UID] || len(g.rows) == 0 {
			return out, errConfigurationOwned
		}
		seenOwners[f.UID] = true
		name := g.rows[0].Operation.ConfigName
		if name == "" {
			return out, errConfigurationOwned
		}
		own := &cvk.NetworkControllerConfigurationStatus{ActionApplications: g.rows}
		for _, section := range tagOwnerSections(f.Scope) {
			own.Owners = append(own.Owners, cvk.NetworkControllerSectionOwner{Section: section, ConfigUID: f.UID, ConfigName: name})
		}
		for _, r := range g.rows {
			if r.Operation.ConfigUID != f.UID || r.Operation.ConfigName != name || r.Section != r.Operation.Section || !containsTagOwnerSection(f.Scope, r.Section) || r.Operation.ConfigGeneration > f.ConfigGeneration || r.Operation.ControllerGeneration > f.ControllerGeneration {
				return out, errConfigurationOwned
			}
		}
		if !tagClosedRowSet(own) || tagOwnerSeal(f, sortedTagOwnerEvidence(own)) != f.Seal {
			return out, errConfigurationOwned
		}
		if err := tagOwnerAnchorsValid(g.rows, out.root, out.lastVerified); err != nil {
			return out, err
		}
		for _, row := range g.rows {
			if row.Operation.Action != "Create" {
				continue
			}
			r := row
			if tagRetiredSelected(r.Resource) {
				r.Resource = strings.TrimPrefix(r.Resource, tagRetiredPrefix)
			}
			_, id, _, err := tagReceiptIdentity(r)
			if err != nil || out.ids[id] {
				return out, errConfigurationOwned
			}
			out.ids[id] = true
		}
		for _, r := range g.rows {
			if r.VerifiedAt != nil && r.VerifiedAt.After(out.lastVerified) {
				out.lastVerified = r.VerifiedAt.Time
			}
		}
		visited[next] = true
		out.rows = append(out.rows, g.wrapped...)
		copy := f
		out.root = &copy
	}
	return out, nil
}
func containsTagOwnerSection(scope, section string) bool {
	for _, s := range tagOwnerSections(scope) {
		if s == section {
			return true
		}
	}
	return false
}
func sortedTagOwnerEvidence(s *cvk.NetworkControllerConfigurationStatus) *cvk.NetworkControllerConfigurationStatus {
	copy := s.DeepCopy()
	sort.Slice(copy.Owners, func(i, j int) bool { return copy.Owners[i].Section < copy.Owners[j].Section })
	sort.Slice(copy.ActionApplications, func(i, j int) bool { return copy.ActionApplications[i].Resource < copy.ActionApplications[j].Resource })
	return copy
}
func tagOwnerAnchorsValid(rows []cvk.NetworkControllerConfigurationActionApplication, prior *tagOwnerFrame, cutoff time.Time) error {
	for _, r := range rows {
		if r.Operation.Action != "Create" || !tagOwnedSelected(r.Operation.Resource) || tagRecreateSelected(r.Operation.Resource) {
			continue
		}
		if prior == nil {
			if tagSuccessorSelected(r.Operation.Resource) {
				return errConfigurationOwned
			}
			continue
		}
		// The predecessor seal binds its entire closed tag namespace. A new
		// owner may use another tag section; the containing owner frame and
		// current custody proof validate that owner's own section claims.
		if !tagSuccessorSelected(r.Operation.Resource) || tagSuccessorSeal(r.Operation.Resource) != prior.Seal || r.Operation.ConfigUID == prior.UID || r.Operation.ControllerGeneration < prior.ControllerGeneration || !containsTagOwnerSection("b", r.Section) || r.Operation.SubmittedAt == nil || r.Operation.SubmittedAt.Before(&metav1.Time{Time: cutoff}) {
			return errConfigurationOwned
		}
	}
	return nil
}
func tagRetiredCohorts(state *cvk.NetworkControllerConfigurationStatus) (map[string]tagRetiredCohort, error) {
	g, err := tagOwnerArchiveGraph(state)
	if err != nil {
		return nil, err
	}
	var current []cvk.NetworkControllerConfigurationActionApplication
	if state != nil {
		for _, r := range state.ActionApplications {
			if !tagOwnerArchivedSelected(r.Resource) {
				current = append(current, r)
			}
		}
	}
	local := &cvk.NetworkControllerConfigurationStatus{ActionApplications: current}
	cohorts, err := tagLocalRetiredCohorts(local)
	if err != nil {
		return nil, err
	}
	if err := tagOwnerAnchorsValid(current, g.root, g.lastVerified); err != nil {
		return nil, err
	}
	for _, r := range current {
		if r.Operation.Action != "Create" || !tagOwnedSelected(r.Operation.Resource) {
			continue
		}
		copy := r
		if tagRetiredSelected(copy.Resource) {
			copy.Resource = strings.TrimPrefix(copy.Resource, tagRetiredPrefix)
		}
		_, id, _, err := tagReceiptIdentity(copy)
		if err != nil || g.ids[id] {
			return nil, errConfigurationOwned
		}
	}
	return cohorts, nil
}
func tagSuccessorIntent(base string, state *cvk.NetworkControllerConfigurationStatus) string {
	g, err := tagOwnerArchiveGraph(state)
	if err != nil {
		return ""
	}
	if g.root == nil {
		return base
	}
	return tagSuccessorPrefix + strings.TrimPrefix(base, tagOwnedPrefix) + ":" + g.root.Seal
}
func tagSuccessorAuthority(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	g, err := tagOwnerArchiveGraph(state)
	if err != nil {
		return err
	}
	if tagRecreateSelected(op.Resource) {
		return nil
	}
	if g.root == nil {
		if tagSuccessorSelected(op.Resource) {
			return errConfigurationOwned
		}
		return nil
	}
	if op.SubmittedAt != nil && op.SubmittedAt.Time.Before(g.lastVerified) {
		return errConfigurationOwned
	}
	if !tagSuccessorSelected(op.Resource) || tagSuccessorSeal(op.Resource) != g.root.Seal || op.ConfigUID == g.root.UID || op.ControllerGeneration < g.root.ControllerGeneration {
		return errConfigurationOwned
	}
	return nil
}
func tagOwnerArchiveNativeAbsent(state *cvk.NetworkControllerConfigurationStatus, rows map[string]observedTag) error {
	g, err := tagOwnerArchiveGraph(state)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if g.ids[row.ID] {
			return errConfigurationOwned
		}
	}
	return nil
}
func (c *client) tagOwnerHistoryAbsent(ctx context.Context, state *cvk.NetworkControllerConfigurationStatus) error {
	g, err := tagOwnerArchiveGraph(state)
	if err != nil {
		return err
	}
	if g.root == nil {
		return nil
	}
	rows, err := c.listTags(ctx)
	if err != nil {
		return err
	}
	return tagOwnerArchiveNativeAbsent(state, rows)
}
func archiveTagOwner(state *cvk.NetworkControllerConfigurationStatus, r *tagOwnershipRetirement) (*tagOwnerFrame, error) {
	g, err := tagOwnerArchiveGraph(state)
	if err != nil {
		return nil, err
	}
	own := &cvk.NetworkControllerConfigurationStatus{}
	carrier := -1
	for i, row := range state.ActionApplications {
		if row.Operation.ConfigUID == r.Completion.ConfigUID && !tagOwnerArchivedSelected(row.Resource) {
			own.ActionApplications = append(own.ActionApplications, row)
			if row.Operation.Action == "Delete" && (carrier < 0 || row.Operation.ID < state.ActionApplications[carrier].Operation.ID) {
				carrier = i
			}
		}
	}
	if len(own.ActionApplications) == 0 {
		return g.root, nil
	}
	for _, s := range r.Sections {
		own.Owners = append(own.Owners, cvk.NetworkControllerSectionOwner{Section: s, ConfigUID: r.Completion.ConfigUID, ConfigName: r.ConfigName})
	}
	own = sortedTagOwnerEvidence(own)
	if carrier < 0 || featureIdentityDigest(own) != r.RetainedDigest {
		return nil, errConfigurationOwned
	}
	f := tagOwnerFrame{Scope: tagOwnerScope(r.Sections), UID: r.Completion.ConfigUID, ConfigGeneration: r.Completion.ConfigGeneration, ControllerGeneration: r.Completion.ControllerGeneration, SpecDigest: r.Completion.SpecDigest, DeletionToken: r.Completion.DeletionToken, PriorDigest: featureCohortDigest(g.rows)}
	f.Seal = tagOwnerSeal(f, own)
	for i, row := range state.ActionApplications {
		if row.Operation.ConfigUID != f.UID || tagOwnerArchivedSelected(row.Resource) {
			continue
		}
		prefix := tagOwnerRetiredPrefix + f.Seal + ":"
		if i == carrier {
			prefix = tagOwnerRootPrefix + tagOwnerFrameText(f) + ":"
		}
		if len(prefix+row.Resource) > 512 {
			return nil, errConfigurationBusy
		}
		state.ActionApplications[i].Resource = prefix + row.Resource
	}
	after, err := tagOwnerArchiveGraph(state)
	if err != nil || after.root == nil || !reflect.DeepEqual(*after.root, f) {
		return nil, errConfigurationOwned
	}
	return &f, nil
}
