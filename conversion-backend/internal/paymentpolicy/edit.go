package paymentpolicy

import (
	"encoding/hex"
	"fmt"
	"slices"
)

func ValidPublicationID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

type Capabilities struct {
	PaymentKinds  []string `json:"payment_kinds"`
	FirstPaid     bool     `json:"first_paid_on_promotion"`
	FirstReason   string   `json:"first_paid_disabled_reason,omitempty"`
	PromotionName string   `json:"promotion_name,omitempty"`
}

type Draft struct {
	App               string      `json:"app_id"`
	Promotion         string      `json:"promotion_id,omitempty"`
	ExpectedVersion   *int64      `json:"expected_version,string"`
	ExpectedInherited *int64      `json:"expected_inherited_version,string,omitempty"`
	Range             string      `json:"payment_range,omitempty"`
	Deactivate        bool        `json:"deactivate,omitempty"`
	Reason            string      `json:"reason,omitempty"`
	PublicationID     string      `json:"publication_id,omitempty"`
	Operations        []Operation `json:"operations"`
}

type Operation struct {
	Type      string      `json:"op"`
	Kind      string      `json:"payment_kind,omitempty"`
	Currency  string      `json:"currency,omitempty"`
	BranchID  int64       `json:"branch_id,string,omitempty"`
	BranchIDs []string    `json:"branch_ids,omitempty"`
	Bands     []BandInput `json:"bands,omitempty"`
	Action    *Action     `json:"action,omitempty"`
}

func applyDraft(base Config, draft Draft, caps Capabilities) (Config, error) {
	if len(draft.Operations) > 128 {
		return Config{}, invalid("invalid_payment_policy", "operations")
	}
	out := base
	out.Groups = slices.Clone(out.Groups)
	for i := range out.Groups {
		out.Groups[i].Bands = slices.Clone(out.Groups[i].Bands)
	}
	if draft.Range != "" {
		out.Range = draft.Range
	}
	switch out.Range {
	case FirstPaid:
		if !caps.FirstPaid {
			return Config{}, invalid(caps.FirstReason, "payment_range")
		}
	case AllPaid:
	default:
		return Config{}, invalid("invalid_payment_policy", "payment_range")
	}
	for i, op := range draft.Operations {
		if !applyOperation(&out, caps, op) {
			return Config{}, invalid("invalid_payment_policy_operation", fmt.Sprintf("operations.%d", i))
		}
	}
	for i := range out.Groups {
		group := &out.Groups[i]
		if len(group.Bands) > 100 || !nonoverlapping(group.Bands) {
			return Config{}, invalid("invalid_payment_policy_bands", "groups."+group.Kind+".bands")
		}
	}
	return out, nil
}

func (c *Config) findGroup(kind string) int {
	return slices.IndexFunc(c.Groups, func(group Group) bool { return group.Kind == kind })
}

func (c *Config) ensureGroup(kind string) *Group {
	i := c.findGroup(kind)
	if i < 0 {
		i = len(c.Groups)
		c.Groups = append(c.Groups, Group{Kind: kind, Bands: []Band{}})
	}
	return &c.Groups[i]
}

func (c *Config) findBand(id int64) (group, band int, ok bool) {
	if id > 0 {
		for i, group := range c.Groups {
			for j, band := range group.Bands {
				if band.ID == id {
					return i, j, true
				}
			}
		}
	}
	return 0, 0, false
}

func makeBands(code string, spec []BandInput) ([]Band, bool) {
	if _, ok := currencyUnit(code); !ok || len(spec) == 0 || len(spec) > 100 {
		return nil, false
	}
	bands := make([]Band, 0, len(spec))
	for _, b := range spec {
		if b.Min == nil || *b.Min < 0 || (b.Max != nil && *b.Max <= *b.Min) || !b.Action.valid() {
			return nil, false
		}
		bands = append(bands, Band{Currency: code, Min: *b.Min, Max: b.Max, Branch: Branch{Action: b.Action}})
	}
	slices.SortFunc(bands, cmpBand)
	return bands, true
}

func applyOperation(c *Config, caps Capabilities, op Operation) bool {
	if op.Kind != "" && !slices.Contains(caps.PaymentKinds, op.Kind) {
		return false
	}
	switch op.Type {
	case "add_band":
		bands, ok := makeBands(op.Currency, op.Bands)
		if !ok || len(bands) != 1 || op.BranchID != 0 || len(op.BranchIDs) != 0 || op.Action != nil {
			return false
		}
		group := c.ensureGroup(op.Kind)
		group.Bands = append(group.Bands, bands[0])
		return true
	case "set_fallback", "remove_fallback", "remove_group":
		return applyGroupOperation(c, op)
	case "merge_bands":
		return applyMerge(c, op)
	case "update_band", "split_band", "remove_band":
		return applyBandOperation(c, op)
	default:
		return false
	}
}

func applyGroupOperation(c *Config, op Operation) bool {
	if op.Currency != "" || op.BranchID != 0 || len(op.BranchIDs) != 0 || len(op.Bands) != 0 {
		return false
	}
	i := c.findGroup(op.Kind)
	if op.Type == "set_fallback" {
		if op.Action == nil || !op.Action.valid() {
			return false
		}
		group := c.ensureGroup(op.Kind)
		fallback := Branch{Action: *op.Action}
		if group.Fallback != nil {
			fallback.ID = group.Fallback.ID
		}
		group.Fallback = &fallback
		return true
	}
	if i < 0 || op.Action != nil {
		return false
	}
	if op.Type == "remove_group" {
		c.Groups = slices.Delete(c.Groups, i, i+1)
		return true
	}
	if c.Groups[i].Fallback == nil {
		return false
	}
	c.Groups[i].Fallback = nil
	return true
}

func applyBandOperation(c *Config, op Operation) bool {
	g, i, ok := c.findBand(op.BranchID)
	if !ok || op.Kind != "" || op.Action != nil || len(op.BranchIDs) != 0 {
		return false
	}
	group := &c.Groups[g]
	old := group.Bands[i]
	if op.Type == "remove_band" {
		if len(op.Bands) != 0 || op.Currency != "" {
			return false
		}
		group.Bands = slices.Delete(group.Bands, i, i+1)
		return true
	}
	code := old.Currency
	if op.Currency != "" {
		code = op.Currency
	}
	bands, ok := makeBands(code, op.Bands)
	if !ok {
		return false
	}
	switch op.Type {
	case "update_band":
		if len(bands) != 1 {
			return false
		}
		if code == old.Currency {
			bands[0].ID = old.ID
		}
	case "split_band":
		if len(bands) < 2 || code != old.Currency || !adjacent(bands) ||
			bands[0].Min != old.Min || !equalPtr(bands[len(bands)-1].Max, old.Max) {
			return false
		}
		bands[0].ID = old.ID
	}
	group.Bands = slices.Replace(group.Bands, i, i+1, bands...)
	return true
}

func applyMerge(c *Config, op Operation) bool {
	if op.Kind != "" || op.Currency != "" || op.BranchID != 0 || op.Action != nil ||
		len(op.BranchIDs) < 2 || len(op.BranchIDs) > 100 {
		return false
	}
	groupIndex := -1
	merged := make([]Band, 0, len(op.BranchIDs))
	ids := make(map[int64]struct{}, len(op.BranchIDs))
	for _, branch := range op.BranchIDs {
		id, ok := positiveID(branch)
		if !ok {
			return false
		}
		g, i, ok := c.findBand(id)
		if _, duplicate := ids[id]; duplicate || !ok || (groupIndex >= 0 && groupIndex != g) {
			return false
		}
		groupIndex = g
		ids[id] = struct{}{}
		merged = append(merged, c.Groups[g].Bands[i])
	}
	slices.SortFunc(merged, cmpBand)
	lowest := merged[0]
	replacement, ok := makeBands(lowest.Currency, op.Bands)
	if !ok || len(replacement) != 1 || !adjacent(merged) ||
		replacement[0].Min != lowest.Min || !equalPtr(replacement[0].Max, merged[len(merged)-1].Max) {
		return false
	}
	replacement[0].ID = lowest.ID
	group := &c.Groups[groupIndex]
	group.Bands = slices.DeleteFunc(group.Bands, func(b Band) bool {
		_, remove := ids[b.ID]
		return remove
	})
	group.Bands = append(group.Bands, replacement[0])
	return true
}

func equalPtr[T comparable](a, b *T) bool {
	return a == b || (a != nil && b != nil && *a == *b)
}
