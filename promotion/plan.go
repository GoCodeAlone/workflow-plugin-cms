package promotion

import (
	"github.com/GoCodeAlone/workflow-plugin-cms/store"
)

type Mapping struct {
	Key       string `json:"key"`
	TargetID  int64  `json:"target_id"` // zero explicitly creates a new page
	AllowMove bool   `json:"allow_move"`
}
type DeleteSelection struct {
	Key      string `json:"key"`
	TargetID int64  `json:"target_id"`
}
type Diff struct {
	Key          string `json:"key"`
	TargetID     int64  `json:"target_id"`
	Action       string `json:"action"`
	OldPath      string `json:"old_path,omitempty"`
	NewPath      string `json:"new_path,omitempty"`
	BeforeDigest string `json:"before_digest,omitempty"`
	AfterDigest  string `json:"after_digest,omitempty"`
}
type Plan struct {
	Digest               string          `json:"digest"`
	SnapshotDigest       string          `json:"snapshot_digest"`
	BundleDigest         string          `json:"bundle_digest"`
	BundleBaselineDigest string          `json:"bundle_baseline_digest"`
	Batch                store.PageBatch `json:"batch"`
	Diffs                []Diff          `json:"diffs"`
}

func (p Plan) ContentDigest() string { p.Digest = ""; return sumJSON(p) }

// DryRun requires an explicit mapping for every selected page. SuggestedMapping
// offers reviewable path matches, but never silently approves a rename/deletion.
// The caller must separately verify both on-disk bundles before publication.
func DryRun(s Snapshot, current store.PageSet, mappings []Mapping, deletes []DeleteSelection, targetBundle BundleManifest) (Plan, error) {
	if err := s.Validate(); err != nil {
		return Plan{}, err
	}
	if targetBundle.Validate() != nil {
		return Plan{}, ErrBundle
	}
	byID := map[int64]store.PageState{}
	for _, p := range current.Pages {
		if p.ID <= 0 || byID[p.ID].ID != 0 {
			return Plan{}, ErrMapping
		}
		byID[p.ID] = p
	}
	byKey := map[string]Mapping{}
	targets := map[int64]bool{}
	for _, m := range mappings {
		if m.Key == "" || m.TargetID < 0 {
			return Plan{}, ErrMapping
		}
		if _, ok := byKey[m.Key]; ok {
			return Plan{}, ErrMapping
		}
		byKey[m.Key] = m
	}
	if len(mappings) != len(s.Pages) {
		return Plan{}, ErrMapping
	}
	plan := Plan{SnapshotDigest: s.Digest, BundleDigest: s.Bundle.Digest, BundleBaselineDigest: targetBundle.Digest, Batch: store.BatchFor(current, nil), Diffs: []Diff{}}
	for _, page := range s.Pages {
		m, ok := byKey[page.Key]
		if !ok {
			return Plan{}, ErrMapping
		}
		content := page.Content
		diff := Diff{Key: page.Key, TargetID: m.TargetID, NewPath: content.Path, AfterDigest: content.Digest()}
		mutation := store.PageMutation{Key: page.Key, TargetID: m.TargetID, Content: &content}
		if m.TargetID == 0 {
			diff.Action = "create"
			mutation.Kind = "create"
		} else {
			p := byID[m.TargetID]
			if p.ID == 0 || targets[p.ID] {
				return Plan{}, ErrMapping
			}
			targets[p.ID] = true
			if (p.Content.Path != content.Path || p.Content.Subsite != content.Subsite) && !m.AllowMove {
				return Plan{}, ErrMapping
			}
			diff.Action = "update"
			diff.OldPath = p.Content.Path
			diff.BeforeDigest = p.Content.Digest()
			mutation.Kind = "update"
			mutation.ExpectedVersion = p.Version
			if diff.BeforeDigest == diff.AfterDigest {
				diff.Action = "unchanged"
			}
		}
		plan.Diffs = append(plan.Diffs, diff)
		if diff.Action != "unchanged" {
			plan.Batch.Mutations = append(plan.Batch.Mutations, mutation)
		}
	}
	for _, d := range deletes {
		p := byID[d.TargetID]
		if d.Key == "" || p.ID == 0 || targets[p.ID] {
			return Plan{}, ErrMapping
		}
		targets[p.ID] = true
		plan.Batch.Mutations = append(plan.Batch.Mutations, store.PageMutation{Key: d.Key, Kind: "delete", TargetID: p.ID, ExpectedVersion: p.Version})
		plan.Diffs = append(plan.Diffs, Diff{Key: d.Key, TargetID: p.ID, Action: "delete", OldPath: p.Content.Path, BeforeDigest: p.Content.Digest()})
	}
	if len(plan.Batch.Mutations) > 0 {
		if err := store.CheckPageBatch(current, plan.Batch, false); err != nil {
			return Plan{}, err
		}
	}
	plan.Digest = plan.ContentDigest()
	return plan, nil
}

func SuggestedMapping(s Snapshot, current store.PageSet) ([]Mapping, error) {
	if s.Validate() != nil {
		return nil, ErrInvalid
	}
	out := []Mapping{}
	for _, page := range s.Pages {
		m := Mapping{Key: page.Key}
		for _, p := range current.Pages {
			if p.Content.Subsite == page.Content.Subsite && p.Content.Path == page.Content.Path {
				if m.TargetID != 0 {
					return nil, ErrMapping
				}
				m.TargetID = p.ID
			}
		}
		out = append(out, m)
	}
	return out, nil
}
