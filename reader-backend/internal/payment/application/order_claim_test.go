package application

import (
	"context"
	"slices"
	"testing"

	"reader-backend/internal/testdb"
)

func TestPayPalOrderClaimPreservesEligibilityOrderAndLocks(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id) VALUES(1);
		INSERT INTO recharge_orders(id,user_id,external_ref_id,amount_cents,payment_method,status,order_type,
			checkout_id,created_at,reconciliation_at,fulfilled_at)
		SELECT id,1,ref,100,method,status,kind,
			CASE WHEN linked THEN gen_random_uuid() END,now()-age,now()-due,
			CASE WHEN fulfilled THEN now() END
		FROM (VALUES
			(1,'locked','paypal','Pending','single',true,interval '1 day',interval '10 days',false),
			(2,'legacy','paypal','Pending','single',false,interval '90 days',interval '9 days',false),
			(3,'whole-paid','paypal','Paid','whole_book',true,interval '90 days',interval '8 days',false),
			(4,'recent-first','paypal','Pending','single',true,interval '20 days',interval '7 days',false),
			(5,'whole-success','paypal','Success','whole_book',false,interval '90 days',interval '6 days',false),
			(6,'recent-tie-first','paypal','Pending','single',true,interval '20 days',interval '5 days',false),
			(7,'recent-tie-second','paypal','Pending','single',true,interval '20 days',interval '5 days',false),
			(8,'old-linked','paypal','Pending','single',true,interval '30 days 1 minute',interval '90 days',false),
			(9,'already-fulfilled','paypal','Paid','whole_book',true,interval '90 days',interval '90 days',true),
			(10,'mock-excluded','paypal','Pending','single',false,interval '90 days',interval '90 days',false),
			(11,'stripe-excluded','stripe','Pending','single',false,interval '90 days',interval '90 days',false),
			(12,'lease-active','paypal','Pending','single',true,interval '1 day',interval '1 minute',false),
			(13,'paid-single','paypal','Paid','single',false,interval '90 days',interval '90 days',false)
		) AS orders(id,ref,method,status,kind,linked,age,due,fulfilled)`); err != nil {
		t.Fatal(err)
	}

	locker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(ctx)
	if _, err = locker.Exec(ctx, "SELECT id FROM recharge_orders WHERE id=1 FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	checkClaim := func(want ...string) {
		t.Helper()
		jobs, err := claimPayPalOrders(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(jobs))
		for _, job := range jobs {
			if job.attempt != 1 {
				t.Fatalf("order %s was claimed more than once: %d", job.id, job.attempt)
			}
			ids = append(ids, job.id)
		}
		slices.Sort(ids)
		if !slices.Equal(ids, want) {
			t.Fatalf("claimed %v, want %v", ids, want)
		}
	}
	checkClaim("legacy", "recent-first", "recent-tie-first", "whole-paid", "whole-success")
	if err = locker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	checkClaim("locked", "recent-tie-second")
	checkClaim()
}
