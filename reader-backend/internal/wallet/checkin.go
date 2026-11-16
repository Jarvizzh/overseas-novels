package wallet

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"reader-backend/internal/db"
)

var checkInRewards = [...]int{10, 15, 20, 25, 30, 35, 50}

type CheckInState struct {
	CheckedIn    bool  `json:"checked_in"`
	Day          int   `json:"day"`
	CoinsAwarded int   `json:"coins_awarded"`
	Rewards      []int `json:"rewards"`
}

func (r *dbRepository) CheckIn(ctx context.Context, userID int64, claim bool) (*CheckInState, error) {
	tx, err := db.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if claim {
		// Serialize check-ins before reading the user's current streak.
		if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, userID); err != nil {
			return nil, err
		}
	}
	var checked bool
	var streak int
	err = tx.QueryRow(ctx, `
		WITH dates AS (
			SELECT (created_at AT TIME ZONE 'UTC')::date AS day
			FROM transactions WHERE user_id = $1 AND biz_type = 'checkin'
		), ranked AS (
			SELECT day, max(day) OVER () AS last_day,
				row_number() OVER (ORDER BY day DESC) - 1 AS day_offset FROM dates
		)
		SELECT COALESCE(bool_or(day = (now() AT TIME ZONE 'UTC')::date), FALSE),
			count(*) FILTER (WHERE last_day >= (now() AT TIME ZONE 'UTC')::date - 1
				AND last_day - day = day_offset)
		FROM ranked`, userID).Scan(&checked, &streak)
	if err != nil {
		return nil, err
	}
	day := (streak+len(checkInRewards)-1)%len(checkInRewards) + 1
	if streak == 0 {
		day = 0
	}
	state := &CheckInState{CheckedIn: checked, Day: day, Rewards: checkInRewards[:]}
	if !claim || checked {
		return state, nil
	}
	day = streak%len(checkInRewards) + 1
	reward := checkInRewards[day-1]
	_, err = tx.Exec(ctx, `INSERT INTO wallets(user_id, bonus_coins) VALUES ($1,$2)
		ON CONFLICT (user_id) DO UPDATE SET bonus_coins=wallets.bonus_coins+$2, updated_at=now()`, userID, reward)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO transactions(id,user_id,type,biz_type,amount,charged_amount,bonus_amount,description)
		VALUES($1,$2,'credit','checkin',$3,0,$3,$4)`, uuid.NewString(), userID, reward, fmt.Sprintf("Daily Check-in (Day %d)", day))
	if err != nil {
		return nil, err
	}
	state.CheckedIn, state.Day, state.CoinsAwarded = true, day, reward
	return state, tx.Commit(ctx)
}
