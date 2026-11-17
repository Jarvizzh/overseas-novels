package tracking

import (
	"context"
	"fmt"
	"strconv"

	"reader-backend/internal/db"
)

func paymentPixelToken(ctx context.Context, pixel string) (string, error) {
	var token string
	if err := db.DB.QueryRow(ctx, `SELECT access_token FROM fb_pixels WHERE pixel_id=$1`, pixel).Scan(&token); err != nil || token == "" {
		return "", fmt.Errorf("payment pixel credentials unavailable")
	}
	return token, nil
}

func savePaymentAttempt(ctx context.Context, event PaymentEvent, test string, status int, payload, response string) error {
	id, err := strconv.ParseInt(event.UserID, 10, 64)
	if err != nil {
		return err
	}
	_, err = db.DB.Exec(ctx, `INSERT INTO facebook_capi_logs(pixel_id,event_name,user_id,value,currency,test_event_code,status_code,payload,response)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, event.Attribution.PixelID, event.Name, id, float64(event.Amount)/100, event.Currency, test, status, payload, response)
	return err
}
