package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUsernameExists = errors.New("Username already exists")
	ErrInvalidLogin   = errors.New("Invalid username or password")
	ErrSuspended      = errors.New("This admin account is suspended")
	ErrAdminNotFound  = errors.New("Admin not found")
)

type AdminService interface {
	Register(ctx context.Context, username, password, nickname, role string) (*Admin, error)
	Login(ctx context.Context, username, password string) (*Admin, string, error)
	GetAdminByID(ctx context.Context, id string) (*Admin, error)
	ListAdmins(ctx context.Context) ([]Admin, error)
	UpdateAdmin(ctx context.Context, id, nickname, role, password string) error
	DeleteAdmin(ctx context.Context, id string) error
}

type adminService struct {
	repo AdminRepository
}

func NewAdminService(repo AdminRepository) AdminService {
	return &adminService{
		repo: repo,
	}
}

func (s *adminService) Register(ctx context.Context, username, password, nickname, role string) (*Admin, error) {
	// Hash password
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	adminID := uuid.New().String()
	admin, err := s.repo.CreateAdmin(ctx, adminID, username, string(hashedBytes), nickname, role)
	if err != nil {
		// Assume database insertion error or duplicate username
		return nil, ErrUsernameExists
	}
	return admin, nil
}

func (s *adminService) Login(ctx context.Context, username, password string) (*Admin, string, error) {
	admin, err := s.repo.GetAdminByUsername(ctx, username)
	if err != nil || admin == nil {
		return nil, "", ErrInvalidLogin
	}

	if admin.Status != 1 {
		return nil, "", ErrSuspended
	}

	err = bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password))
	if err != nil {
		return nil, "", ErrInvalidLogin
	}

	token, err := GenerateToken(admin.ID, admin.Role)
	if err != nil {
		return nil, "", err
	}

	return admin, token, nil
}

func (s *adminService) GetAdminByID(ctx context.Context, id string) (*Admin, error) {
	admin, err := s.repo.GetAdminByID(ctx, id)
	if err != nil || admin == nil {
		return nil, ErrAdminNotFound
	}
	return admin, nil
}

func (s *adminService) ListAdmins(ctx context.Context) ([]Admin, error) {
	return s.repo.ListAdmins(ctx)
}

func (s *adminService) UpdateAdmin(ctx context.Context, id, nickname, role, password string) error {
	exists, err := s.repo.AdminExists(ctx, id)
	if err != nil || !exists {
		return ErrAdminNotFound
	}

	var passwordHash string
	if password != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		passwordHash = string(hashed)
	}

	return s.repo.UpdateAdmin(ctx, id, nickname, role, passwordHash)
}

func (s *adminService) DeleteAdmin(ctx context.Context, id string) error {
	return s.repo.DeleteAdmin(ctx, id)
}
