package wallet

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"reader-backend/internal/tracking"
)

type paymentEventJob struct {
	id, lease string
	payload   []byte
}

func (s *service) reconcilePaymentEvents(ctx context.Context) {
	jobs, err := claimPaymentEvents(ctx)
	if err != nil {
		log.Printf("Payment event claim failed: %v", err)
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		var event tracking.PaymentEvent
		err := json.Unmarshal(job.payload, &event)
		if err == nil {
			request, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = tracking.DeliverPayment(request, event)
			cancel()
		}
		if err != nil {
			log.Printf("task=outbox id=%s error=%v", job.id, err)
		}
		if saveErr := finishPaymentEvent(ctx, job, err); saveErr != nil {
			log.Printf("task=outbox id=%s action=save error=%v", job.id, saveErr)
		}
	}
}
