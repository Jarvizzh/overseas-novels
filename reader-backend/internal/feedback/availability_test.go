package feedback

import (
	"context"
	"reader-backend/internal/testdb"
	"testing"
)

func TestAnonymousFeedbackUsesNullUser(t *testing.T) {
	p := testdb.Open(t)
	id, err := NewService(NewRepository()).SubmitFeedback(context.Background(), 0, &CreateFeedbackRequest{Content: "Help"})
	if err != nil {
		t.Fatal(err)
	}
	var anonymous bool
	if err := p.QueryRow(context.Background(), "SELECT user_id IS NULL FROM feedback WHERE id=$1", id).Scan(&anonymous); err != nil || !anonymous {
		t.Fatal("anonymous feedback not saved", err)
	}
}
