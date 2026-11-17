package auth

import (
	"context"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"reader-backend/internal/db"
	"reader-backend/internal/model"
	"reader-backend/internal/tracking"
	"reader-backend/internal/workerpool"
)

var (
	ErrAlreadyBound    = errors.New("Account is already registered")
	ErrEmailExists     = errors.New("Email is already registered")
	ErrInvalidAuth     = errors.New("Invalid email or password")
	ErrAccountBanned   = errors.New("This account has been banned")
	ErrUserNotFound    = errors.New("User not found")
	ErrInvalidPassword = errors.New("password must contain at least 6 characters and at most 72 bytes")
)

type AuthService interface {
	GuestLogin(ctx context.Context, device, ipAddress, utmSource, utmCampaign, fbp, fbc, pixelID, userAgent, sourceURL, country string) (*model.User, string, error)
	Register(ctx context.Context, email, password, nickname, device, ipAddress, utmSource, utmCampaign, fbp, fbc, pixelID, userAgent, sourceURL, country string) (*model.User, string, error)
	Login(ctx context.Context, email, password string) (*model.User, string, error)
	GetProfile(ctx context.Context, userID int64) (*model.User, error)
	BindEmail(ctx context.Context, userID int64, email, password, nickname string) (*model.User, string, error)
}

type authService struct {
	repo UserRepository
}

func NewAuthService(repo UserRepository) AuthService {
	return &authService{
		repo: repo,
	}
}

func (s *authService) GuestLogin(ctx context.Context, device, ipAddress, utmSource, utmCampaign, fbp, fbc, pixelID, userAgent, sourceURL, country string) (*model.User, string, error) {
	tempSuffix := uuid.New().String()[:8]
	nickname := "Guest_" + tempSuffix

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)

	// 1. Create user
	user, err := s.repo.CreateGuestUser(ctx, tx, nickname, device, ipAddress, utmSource, utmCampaign)
	if err != nil {
		return nil, "", err
	}

	// 2. Initialize wallet with 200 welcome bonus coins
	err = s.repo.InitWallet(ctx, tx, user.ID, 200)
	if err != nil {
		return nil, "", err
	}

	// 3. Create transaction record
	txID := uuid.New().String()
	err = s.repo.CreateTransactionRecord(ctx, tx, txID, user.ID, "credit", "reward_task", 200, 0, 200, "Welcome Reward Coins")
	if err != nil {
		return nil, "", err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, "", err
	}

	token, err := GenerateToken(user.ID)
	if err != nil {
		return nil, "", err
	}

	// Trigger FB CompleteRegistration event asynchronously via WorkerPool strictly using CMS configured pixel
	workerpool.Submit(func() {
		taskCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		effectivePixelID := ""
		if effectivePixelID == "" && utmSource != "" && utmCampaign != "" && db.DB != nil {
			_ = db.DB.QueryRow(taskCtx, "SELECT fp.pixel_id FROM reader_promotion_for_user($1) pl JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id", user.ID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "CompleteRegistration", strconv.FormatInt(user.ID, 10), "", ipAddress, userAgent, fbc, fbp, 0, "", sourceURL, country)
		}
	})

	return user, token, nil
}

func (s *authService) Register(ctx context.Context, email, password, nickname, device, ipAddress, utmSource, utmCampaign, fbp, fbc, pixelID, userAgent, sourceURL, country string) (*model.User, string, error) {
	if utf8.RuneCountInString(password) < 6 || len(password) > 72 {
		return nil, "", ErrInvalidPassword
	}
	exists, err := s.repo.EmailExists(ctx, email)
	if err != nil {
		return nil, "", err
	}
	if exists {
		return nil, "", ErrEmailExists
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}

	if nickname == "" {
		nickname = "User_" + uuid.New().String()[:8]
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)

	// 1. Create user
	user, err := s.repo.CreateStandardUser(ctx, tx, email, string(hashedPassword), nickname, device, ipAddress, utmSource, utmCampaign)
	if err != nil {
		return nil, "", err
	}

	// 2. Initialize wallet with 200 welcome bonus coins
	err = s.repo.InitWallet(ctx, tx, user.ID, 200)
	if err != nil {
		return nil, "", err
	}

	// 3. Create transaction record
	txID := uuid.New().String()
	err = s.repo.CreateTransactionRecord(ctx, tx, txID, user.ID, "credit", "reward_task", 200, 0, 200, "Welcome Reward Coins")
	if err != nil {
		return nil, "", err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, "", err
	}

	token, err := GenerateToken(user.ID)
	if err != nil {
		return nil, "", err
	}

	// Trigger FB CompleteRegistration event asynchronously via WorkerPool strictly using CMS configured pixel
	workerpool.Submit(func() {
		taskCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		effectivePixelID := ""
		if effectivePixelID == "" && utmSource != "" && utmCampaign != "" && db.DB != nil {
			_ = db.DB.QueryRow(taskCtx, "SELECT fp.pixel_id FROM reader_promotion_for_user($1) pl JOIN fb_pixels fp ON pl.fb_pixel_id = fp.id", user.ID).Scan(&effectivePixelID)
		}
		if effectivePixelID != "" {
			tracking.SendFacebookEvent(effectivePixelID, "CompleteRegistration", strconv.FormatInt(user.ID, 10), email, ipAddress, userAgent, fbc, fbp, 0, "", sourceURL, country)
		}
	})

	return user, token, nil
}

func (s *authService) Login(ctx context.Context, email, password string) (*model.User, string, error) {
	if len(password) > 72 {
		return nil, "", ErrInvalidAuth
	}
	user, passwordHash, err := s.repo.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrInvalidAuth
	}
	if err != nil {
		return nil, "", err
	}
	if user == nil {
		return nil, "", ErrInvalidAuth
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, "", ErrInvalidAuth
	}

	if user.Status == 2 {
		return nil, "", ErrAccountBanned
	}

	token, err := GenerateToken(user.ID)
	if err != nil {
		return nil, "", err
	}

	return user, token, nil
}

func (s *authService) GetProfile(ctx context.Context, userID int64) (*model.User, error) {
	user, err := s.repo.GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	return user, nil
}

func (s *authService) BindEmail(ctx context.Context, userID int64, email, password, nickname string) (*model.User, string, error) {
	if utf8.RuneCountInString(password) < 6 || len(password) > 72 {
		return nil, "", ErrInvalidPassword
	}
	current, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if current.Email != nil {
		return nil, "", ErrAlreadyBound
	}
	exists, err := s.repo.EmailExists(ctx, email)
	if err != nil {
		return nil, "", err
	}
	if exists {
		return nil, "", ErrEmailExists
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}

	user, err := s.repo.BindEmail(ctx, userID, email, string(hashedPassword), nickname)
	if err != nil {
		return nil, "", err
	}

	token, err := GenerateToken(user.ID)
	if err != nil {
		return nil, "", err
	}

	return user, token, nil
}
