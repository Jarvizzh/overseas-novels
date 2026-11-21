package paymentpolicy

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
)

var ErrVersionConflict = errors.New("payment_policy_version_conflict")

type Version struct {
	ID     int64  `json:"id,string"`
	Number int64  `json:"number,string"`
	Config Config `json:"config"`
	scope  int64
}

type View struct {
	Scope
	Capabilities
	Source           string       `json:"config_source"`
	OwnVersion       *Version     `json:"own_version"`
	InheritedVersion *Version     `json:"inherited_version"`
	Config           Config       `json:"config"`
	Branches         []BranchView `json:"branches"`
}

type Preview struct {
	Current     View         `json:"current"`
	NextVersion *int64       `json:"next_version_number,string,omitempty"`
	Source      string       `json:"config_source"`
	Config      Config       `json:"config"`
	Branches    []BranchView `json:"branches"`
}

type BranchView struct {
	ID           int64  `json:"branch_id,string,omitempty"`
	Kind         string `json:"payment_kind"`
	Currency     string `json:"currency,omitempty"`
	Min          *int64 `json:"min_minor,string,omitempty"`
	Inherited    bool   `json:"retains_branch_id"`
	NextSequence *int64 `json:"next_sequence,string,omitempty"`
}

func positiveID(raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == raw
}

func settingsTx(ctx context.Context, tx pgx.Tx, s Scope, publish bool) (View, error) {
	const integration = `SELECT source,account_key,allowed_apps FROM conversion.integrations WHERE id=$1 AND environment=$2`
	query := integration
	if publish {
		query += " FOR UPDATE"
	}
	var source, account string
	var apps []string
	if err := tx.QueryRow(ctx, query, s.IntegrationID, s.Environment).Scan(&source, &account, &apps); err != nil {
		return View{}, err
	}
	caps, err := capabilitiesTx(ctx, tx, s, source, account, apps)
	if err != nil {
		return View{}, err
	}
	own, inherited, err := versionsTx(ctx, tx, s)
	if err != nil {
		return View{}, err
	}
	view := View{Scope: s, Capabilities: caps, OwnVersion: own, InheritedVersion: inherited}
	view.Config, view.Source = effective(own, inherited)
	view.Branches, err = branchViews(ctx, tx, s, view.Config)
	return view, err
}

func capabilitiesTx(ctx context.Context, tx pgx.Tx, s Scope, source, account string, apps []string) (Capabilities, error) {
	switch {
	case source == "starnovel" && account == "reader":
		if s.App != ReaderApp {
			return Capabilities{}, invalid("invalid_payment_policy_app", "app_id")
		}
		out := Capabilities{
			PaymentKinds: []string{"single", "whole_book", "subscription_initial", "subscription_renewal"},
			FirstReason:  "payment_policy_first_requires_promotion",
		}
		if s.Promotion == "" {
			return out, nil
		}
		id, ok := positiveID(s.Promotion)
		if !ok || id > 1<<31-1 {
			return Capabilities{}, invalid("invalid_payment_policy_promotion", "promotion_id")
		}
		err := tx.QueryRow(ctx, `
			SELECT reader_payment_coverage_verified($2,p.id),COALESCE(NULLIF(p.name,''),p.novel_title)
			FROM promotion_links p WHERE p.id=$1`, id, s.Environment).Scan(&out.FirstPaid, &out.PromotionName)
		if errors.Is(err, pgx.ErrNoRows) {
			return Capabilities{}, invalid("invalid_payment_policy_promotion", "promotion_id")
		}
		if out.FirstPaid {
			out.FirstReason = ""
		} else {
			out.FirstReason = "payment_policy_first_coverage_unverified"
		}
		return out, err
	case source == "sinan":
		_, validApp := positiveID(s.App)
		if !validApp || !slices.Contains(apps, s.App) {
			return Capabilities{}, invalid("invalid_payment_policy_app", "app_id")
		}
		if s.Promotion != "" {
			if _, ok := positiveID(s.Promotion); !ok {
				return Capabilities{}, invalid("invalid_payment_policy_promotion", "promotion_id")
			}
		}
		return Capabilities{PaymentKinds: []string{"1", "2"}, FirstReason: "payment_policy_first_source_unsupported"}, nil
	default:
		return Capabilities{}, invalid("payment_policy_source_unsupported", "integration_id")
	}
}

func versionsTx(ctx context.Context, tx pgx.Tx, s Scope) (own, inherited *Version, err error) {
	rows, err := tx.Query(ctx, `
		SELECT s.scope_kind,v.scope_id,v.id,v.version_no,v.config
		FROM conversion.payment_policy_scopes s
		JOIN conversion.payment_policy_versions v ON v.scope_id=s.id AND v.id=s.active_version_id
		WHERE s.environment=$1 AND s.integration_id=$2 AND s.app_scope=$3
		  AND ((s.scope_kind='default' AND s.promotion_key='') OR
		       (s.scope_kind='promotion' AND s.promotion_key=$4))`,
		s.Environment, s.IntegrationID, s.App, s.Promotion)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v := new(Version)
		var kind string
		if err := rows.Scan(&kind, &v.scope, &v.ID, &v.Number, &v.Config); err != nil {
			return nil, nil, err
		}
		if kind == s.kind() {
			own = v
		} else {
			inherited = v
		}
	}
	return own, inherited, rows.Err()
}

func effective(own, inherited *Version) (Config, string) {
	switch {
	case own != nil:
		return own.Config, "own"
	case inherited != nil:
		return inherited.Config, "inherited"
	default:
		return defaultConfig(), "implicit_all"
	}
}

func versionID(version *Version) *int64 {
	if version == nil {
		return nil
	}
	return &version.ID
}

func branchViews(ctx context.Context, tx pgx.Tx, scope Scope, config Config) ([]BranchView, error) {
	branches := make([]BranchView, 0)
	var cycleIDs []int64
	var cycleIndices []int
	for _, group := range config.Groups {
		add := func(branch Branch, currency string, min *int64) {
			view := BranchView{ID: branch.ID, Kind: group.Kind, Currency: currency, Min: min, Inherited: branch.ID != 0}
			if branch.Mode == Cycle {
				one := int64(1)
				view.NextSequence = &one
				if branch.ID != 0 {
					cycleIDs = append(cycleIDs, branch.ID)
					cycleIndices = append(cycleIndices, len(branches))
				}
			}
			branches = append(branches, view)
		}
		for _, band := range group.Bands {
			add(band.Branch, band.Currency, &band.Min)
		}
		if group.Fallback != nil {
			add(*group.Fallback, "", nil)
		}
	}
	if len(cycleIDs) == 0 {
		return branches, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT branch_id,last_sequence FROM conversion.payment_policy_counters
		WHERE environment=$1 AND integration_id=$2 AND app_scope=$3 AND scope_kind=$4
		  AND promotion_key=$5 AND branch_id=ANY($6::bigint[])`,
		scope.Environment, scope.IntegrationID, scope.App, scope.kind(), scope.Promotion, cycleIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sequences := make(map[int64]int64, len(cycleIDs))
	for rows.Next() {
		var id, last int64
		if err := rows.Scan(&id, &last); err != nil {
			return nil, err
		}
		sequences[id] = last
	}
	for _, i := range cycleIndices {
		if last := sequences[branches[i].ID]; last < MaxSequence {
			next := last + 1
			branches[i].NextSequence = &next
		} else {
			branches[i].NextSequence = nil
		}
	}
	return branches, rows.Err()
}

func ReadTx(ctx context.Context, tx pgx.Tx, scope Scope) (View, error) {
	return settingsTx(ctx, tx, scope, false)
}

func prepareDraft(ctx context.Context, tx pgx.Tx, current View, draft Draft) (Preview, error) {
	if !equalPtr(draft.ExpectedVersion, versionID(current.OwnVersion)) ||
		((current.OwnVersion == nil || draft.Deactivate) && !equalPtr(draft.ExpectedInherited, versionID(current.InheritedVersion))) {
		return Preview{}, ErrVersionConflict
	}
	var out Preview
	out.Current = current
	if draft.Deactivate {
		if current.Promotion == "" || current.OwnVersion == nil || len(draft.Operations) != 0 || draft.Range != "" {
			return Preview{}, invalid("invalid_payment_policy", "deactivate")
		}
		out.Config, out.Source = effective(nil, current.InheritedVersion)
	} else {
		config, err := applyDraft(current.Config, draft, current.Capabilities)
		if err != nil {
			return Preview{}, err
		}
		out.Config, out.Source = config, "own"
	}
	var err error
	out.Branches, err = branchViews(ctx, tx, current.Scope, out.Config)
	return out, err
}

func PreviewTx(ctx context.Context, tx pgx.Tx, scope Scope, draft Draft) (Preview, error) {
	current, err := ReadTx(ctx, tx, scope)
	if err != nil {
		return Preview{}, err
	}
	out, err := prepareDraft(ctx, tx, current, draft)
	if err != nil || draft.Deactivate {
		return out, err
	}
	var next int64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(v.version_no),0)+1 FROM conversion.payment_policy_scopes s
		JOIN conversion.payment_policy_versions v ON v.scope_id=s.id
		WHERE s.environment=$1 AND s.integration_id=$2 AND s.app_scope=$3
		  AND s.scope_kind=$4 AND s.promotion_key=$5`,
		scope.Environment, scope.IntegrationID, scope.App, scope.kind(), scope.Promotion).Scan(&next)
	if err != nil {
		return Preview{}, err
	}
	out.NextVersion = &next
	return out, nil
}

func PublishTx(ctx context.Context, tx pgx.Tx, scope Scope, draft Draft, actor string) (before View, after View, err error) {
	before, err = settingsTx(ctx, tx, scope, true)
	if err != nil {
		return View{}, View{}, err
	}
	preview, err := prepareDraft(ctx, tx, before, draft)
	if err != nil {
		return View{}, View{}, err
	}
	if draft.Deactivate {
		_, err = tx.Exec(ctx, `UPDATE conversion.payment_policy_scopes SET active_version_id=NULL WHERE id=$1`, before.OwnVersion.scope)
	} else {
		err = createVersionTx(ctx, tx, scope, actor, preview.Config)
	}
	if err != nil {
		return View{}, View{}, err
	}
	after, err = ReadTx(ctx, tx, scope)
	return before, after, err
}

func createVersionTx(ctx context.Context, tx pgx.Tx, s Scope, actor string, config Config) error {
	newIDs := make([]*int64, 0)
	for i := range config.Groups {
		group := &config.Groups[i]
		if group.Fallback != nil && group.Fallback.ID == 0 {
			newIDs = append(newIDs, &group.Fallback.ID)
		}
		for j := range group.Bands {
			id := &group.Bands[j].ID
			if *id == 0 {
				newIDs = append(newIDs, id)
			}
		}
	}
	if len(newIDs) != 0 {
		rows, err := tx.Query(ctx, `
			SELECT nextval('conversion.payment_policy_branch_ids') FROM generate_series(1,$1::integer)`, len(newIDs))
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		for i, id := range ids {
			*newIDs[i] = id
		}
	}
	var scopeID int64
	// Publication holds the integration row lock, including when this scope
	// does not have a row yet.
	err := tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO conversion.payment_policy_scopes (environment,integration_id,app_scope,scope_kind,promotion_key)
			VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING id
		)
		SELECT id FROM inserted
		UNION ALL
		SELECT id FROM conversion.payment_policy_scopes
		WHERE environment=$1 AND integration_id=$2 AND app_scope=$3 AND scope_kind=$4 AND promotion_key=$5`,
		s.Environment, s.IntegrationID, s.App, s.kind(), s.Promotion).Scan(&scopeID)
	if err != nil {
		return err
	}
	var versionID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO conversion.payment_policy_versions (scope_id,version_no,config,published_by)
		SELECT $1,COALESCE(MAX(version_no),0)+1,$2,$3 FROM conversion.payment_policy_versions WHERE scope_id=$1
		RETURNING id`, scopeID, config, actor).Scan(&versionID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE conversion.payment_policy_scopes SET active_version_id=$2 WHERE id=$1`, scopeID, versionID)
	return err
}
