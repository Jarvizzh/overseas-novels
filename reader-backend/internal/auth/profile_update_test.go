package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reader-backend/internal/testdb"
)

func TestUpdateProfileNicknameEmailAndPassword(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	svc := NewAuthService(NewUserRepository())

	guest, _, err := svc.GuestLogin(ctx, "dev", "127.0.0.1", "", "", "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Guest can update nickname
	nick := "ReaderOne"
	updatedGuest, rotated, err := svc.UpdateProfile(ctx, guest.ID, &nick, nil, "", "")
	if err != nil || rotated || updatedGuest.Nickname != "ReaderOne" {
		t.Fatalf("guest nickname update failed: user=%+v rotated=%v err=%v", updatedGuest, rotated, err)
	}
	// Guest cannot change email without binding
	emailTry := "guest-new@example.invalid"
	if _, _, err := svc.UpdateProfile(ctx, guest.ID, nil, &emailTry, "secret123", ""); !errors.Is(err, ErrBindRequired) {
		t.Fatalf("expected ErrBindRequired for guest email change, got %v", err)
	}

	userA, _, err := svc.Register(ctx, "a@example.invalid", "password123", "UserA", "dev", "127.0.0.1", "", "", "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Register(ctx, "b@example.invalid", "password123", "UserB", "dev", "127.0.0.1", "", "", "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	session := sessionService{}
	raw, err := session.create(ctx, userA.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Wrong current password rejects email or password update
	dupEmail := "b@example.invalid"
	newEmail := "a-updated@example.invalid"
	if _, _, err := svc.UpdateProfile(ctx, userA.ID, nil, &newEmail, "wrongpass", ""); !errors.Is(err, ErrInvalidCurrentPassword) {
		t.Fatalf("expected ErrInvalidCurrentPassword, got %v", err)
	}
	// Existing email rejects update
	if _, _, err := svc.UpdateProfile(ctx, userA.ID, nil, &dupEmail, "password123", ""); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("expected ErrEmailExists, got %v", err)
	}

	// Valid nickname + email + password update revokes old session
	newNick := "UserAUpdated"
	updatedA, rotated, err := svc.UpdateProfile(ctx, userA.ID, &newNick, &newEmail, "password123", "newpass456")
	if err != nil || !rotated || updatedA.Nickname != newNick || updatedA.Email == nil || *updatedA.Email != newEmail {
		t.Fatalf("expected rotated update, got user=%+v rotated=%v err=%v", updatedA, rotated, err)
	}
	if _, err := session.refresh(ctx, raw); err == nil {
		t.Fatal("expected sensitive profile change to revoke existing sessions")
	}

	// Old password fails login, new password & email succeeds
	if _, _, err := svc.Login(ctx, newEmail, "password123"); !errors.Is(err, ErrInvalidAuth) {
		t.Fatalf("old password should fail login, got %v", err)
	}
	loggedIn, _, err := svc.Login(ctx, newEmail, "newpass456")
	if err != nil || loggedIn.ID != userA.ID {
		t.Fatalf("login with updated email and password failed: %v", err)
	}

	// Verify 255-character email boundary round-trips without VARCHAR(100) truncation
	boundaryEmail := strings.Repeat("u", 60) + "@" + strings.Repeat("d", 63) + "." +
		strings.Repeat("e", 63) + "." + strings.Repeat("f", 59) + ".sample"
	updatedBoundary, rotatedBoundary, err := svc.UpdateProfile(ctx, userA.ID, nil, &boundaryEmail, "newpass456", "")
	if err != nil || !rotatedBoundary || updatedBoundary.Email == nil || *updatedBoundary.Email != boundaryEmail {
		t.Fatalf("255-character email update failed/truncated: user=%+v rotated=%v err=%v", updatedBoundary, rotatedBoundary, err)
	}
	_ = pool
}
