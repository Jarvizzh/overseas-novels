package delivery

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func testAuditedOperations(t *testing.T, ctx context.Context, s Service) {
	t.Helper()
	mustExec(t, ctx, s.Pool, `
		INSERT INTO admins(id,username,password_hash,role,role_id,status) VALUES
			('ops','ops','fixture','Admin',(SELECT id FROM roles WHERE code='Admin'),1),
			('optimizer','optimizer','fixture','AdOptimizer',(SELECT id FROM roles WHERE code='AdOptimizer'),1),
			('reader','reader','fixture','FinanceManager',(SELECT id FROM roles WHERE code='FinanceManager'),1);
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,fb_lead_metadata)
		VALUES(400,10,999,'paypal','Success',now(),
		  '{"pixel_id":"123","source_url":"https://example.invalid/checkout","user_agent":"fixture"}');
		CREATE FUNCTION omit_reader_outbox_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
		CREATE TRIGGER omit_reader_outbox_fixture BEFORE INSERT ON reader_conversion_outbox
		FOR EACH ROW EXECUTE FUNCTION omit_reader_outbox_fixture();
		SELECT record_reader_payment_conversion(400,now()-interval '20 seconds','paypal_capture_create_time','test',false,false);
		DROP TRIGGER omit_reader_outbox_fixture ON reader_conversion_outbox;
		DROP FUNCTION omit_reader_outbox_fixture();`)
	operator := Operator{ID: "ops", Permission: PermissionAdsConversionsWrite}
	req := RepairRequest{OrderID: 400, EventName: "Purchase"}
	dry, err := s.RepairMissing(ctx, operator, req)
	if err != nil || dry.Action != "restore_outbox" || !dry.DryRun {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	assertString(t, ctx, s.Pool, "SELECT count(*)::text FROM conversion.operation_audit", "0")
	assertString(t, ctx, s.Pool, "SELECT count(*)::text FROM reader_conversion_outbox WHERE business_key='payment:400:Purchase'", "0")
	req.Apply, req.Reason, req.Evidence = true, "  original settlement proof  ", "receipt fixture"
	if _, err = s.RepairMissing(ctx, Operator{ID: "reader", Permission: PermissionAdsConversionsWrite}, req); !errors.Is(err, ErrOperatorForbidden) {
		t.Fatalf("role guard: %v", err)
	}
	if result, err := s.RepairMissing(ctx, Operator{ID: "optimizer", Permission: PermissionAdsConversionsWrite}, req); err != nil || result.Existing {
		t.Fatalf("optimizer repair: %+v %v", result, err)
	}
	assertString(t, ctx, s.Pool, "SELECT operator_role FROM conversion.operation_audit ORDER BY id DESC LIMIT 1", "AdOptimizer")
	if result, err := s.RepairMissing(ctx, operator, req); err != nil || !result.Existing {
		t.Fatalf("replay: %+v %v", result, err)
	}
	if count, err := s.ConsumeReader(ctx, 1); err != nil || count != 1 {
		t.Fatalf("normalize: %d %v", count, err)
	}
	var eventID, original int64
	err = s.Pool.QueryRow(ctx, `
		SELECT e.id,d.id FROM conversion.events e JOIN conversion.deliveries d ON d.event_id=e.id AND d.role='original'
		WHERE e.source_event_id='payment:400:Purchase'`).Scan(&eventID, &original)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, s.Pool, `DELETE FROM conversion.deliveries WHERE event_id=(SELECT id FROM conversion.events
		WHERE source_event_id='payment:400:Purchase') AND role='original'`)
	req.Apply = false
	if out, err := s.RepairMissing(ctx, operator, req); err != nil || out.Action != "restore_original" || !out.DryRun {
		t.Fatalf("plan original repair: %+v, %v", out, err)
	}
	req.Apply = true
	if out, err := s.RepairMissing(ctx, operator, req); err != nil || out.Existing {
		t.Fatalf("restore original: %+v %v", out, err)
	}
	var extraTarget int64
	if err := s.Pool.QueryRow(ctx, "SELECT id FROM conversion.destinations WHERE pixel_id='789'").Scan(&extraTarget); err != nil {
		t.Fatal(err)
	}
	first, err := s.AppendTarget(ctx, operator, eventID, extraTarget, "approved extra target")
	if err != nil || first.Existing {
		t.Fatalf("append: %+v %v", first, err)
	}
	second, err := s.AppendTarget(ctx, operator, eventID, extraTarget, "repeat")
	if err != nil || !second.Existing || first.ID != second.ID {
		t.Fatalf("append replay: %+v %v", second, err)
	}

	mustExec(t, ctx, s.Pool, `
		UPDATE conversion.deliveries SET attempts=1,state='outcome_unknown',unknown_attempt_no=1
		WHERE event_id=(SELECT id FROM conversion.events WHERE source_event_id='payment:400:Purchase') AND role='original';`)
	if err := s.Pool.QueryRow(ctx, "SELECT id FROM conversion.deliveries WHERE event_id=$1 AND role='original'", eventID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryDelivery(ctx, operator, original, 1, "retry", false); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("risk guard: %v", err)
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Go(func() { _, err := s.RetryDelivery(ctx, operator, original, 1, "retry with risk", true); results <- err })
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrOperationConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("winning retry transactions: %d", successes)
	}
	assertString(t, ctx, s.Pool, "SELECT count(*)::text FROM conversion.operation_audit WHERE action='retry_delivery'", "1")

	// A separately allocated settlement lost its Outbox after insertion.
	mustExec(t, ctx, s.Pool, `
		INSERT INTO recharge_orders(id,user_id,amount_cents,payment_method,status,fulfilled_at,fb_lead_metadata)
		SELECT 401,user_id,amount_cents,payment_method,status,fulfilled_at,fb_lead_metadata FROM recharge_orders WHERE id=400;
		SELECT record_reader_payment_conversion(401,now()-interval '20 seconds','paypal_capture_create_time','test',false,false);
		DELETE FROM reader_conversion_outbox WHERE business_key='payment:401:Purchase';`)
	lost := req
	lost.OrderID, lost.Apply = 401, false
	if result, err := s.RepairMissing(ctx, operator, lost); err != nil || result.Action != "restore_outbox" || !result.DryRun {
		t.Fatalf("lost row plan: %+v %v", result, err)
	}

	// The attempted-send marker must survive deletion of the attempt and target.
	mustExec(t, ctx, s.Pool, `
		INSERT INTO conversion.delivery_attempts(delivery_id,attempt_no,lease_token)
		SELECT d.id,1,gen_random_uuid() FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
		WHERE e.source_event_id='payment:400:Purchase' AND d.role='original';
		UPDATE conversion.delivery_attempts a SET sending_at=now()
		FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
		WHERE a.delivery_id=d.id AND e.source_event_id='payment:400:Purchase' AND d.role='original';
		DELETE FROM conversion.delivery_attempts a USING conversion.deliveries d,conversion.events e
		WHERE a.delivery_id=d.id AND d.event_id=e.id AND e.source_event_id='payment:400:Purchase' AND d.role='original';
		DELETE FROM conversion.deliveries d USING conversion.events e
		WHERE d.event_id=e.id AND e.source_event_id='payment:400:Purchase' AND d.role='original';`)
	if _, err := s.RepairMissing(ctx, operator, req); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("attempted event could be repaired: %v", err)
	}
	assertString(t, ctx, s.Pool, "SELECT count(*)::text FROM conversion.operation_audit", "4")
}
