package delivery

import (
	"context"
	"strconv"

	"conversion-backend/internal/paymentpolicy"

	"github.com/jackc/pgx/v5"
)

func (s Service) PublishPaymentPolicy(ctx context.Context, actor Operator, id int64, draft paymentpolicy.Draft) (paymentpolicy.View, error) {
	if draft.PublicationID != "" && !paymentpolicy.ValidPublicationID(draft.PublicationID) {
		return paymentpolicy.View{}, &paymentpolicy.ValidationError{Code: "invalid_payment_policy_request", Field: "publication_id"}
	}
	actor, err := requireBusinessPermission(actor, PermissionAdsConversionsWrite)
	if err != nil {
		return paymentpolicy.View{}, err
	}
	var out paymentpolicy.View
	_, err = s.withOperatorTx(ctx, actor, func(tx pgx.Tx, actor Operator) (OperationResult, error) {
		scope := paymentpolicy.Scope{Environment: s.Environment, IntegrationID: id, App: draft.App, Promotion: draft.Promotion}
		before, after, err := paymentpolicy.PublishTx(ctx, tx, scope, draft, actor.ID)
		if err != nil {
			return OperationResult{}, err
		}
		action := "publish_payment_policy"
		if draft.Deactivate {
			action = "inherit_payment_policy"
		}
		reason := "变更回传规则"
		if after.OwnVersion != nil && after.OwnVersion.Number == 1 {
			reason = "新建回传规则"
		}
		auditState := func(view paymentpolicy.View) map[string]any {
			return map[string]any{"app_id": view.App, "promotion_id": view.Promotion, "config_source": view.Source,
				"own_version": view.OwnVersion, "inherited_version": view.InheritedVersion}
		}
		afterState := auditState(after)
		if draft.PublicationID != "" {
			afterState["publication_id"] = draft.PublicationID
		}
		if err := s.audit(ctx, tx, actor, action, strconv.FormatInt(id, 10), reason, "", auditState(before), afterState); err != nil {
			return OperationResult{}, err
		}
		out = after
		return OperationResult{}, nil
	})
	return out, err
}
