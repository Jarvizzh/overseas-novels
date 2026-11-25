package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUsernameExists  = errors.New("用户名已存在")
	ErrInvalidLogin    = errors.New("用户名或密码错误")
	ErrSuspended       = errors.New("该管理员账号或所属角色已被禁用")
	ErrAdminNotFound   = errors.New("管理员不存在")
	ErrInvalidAdminReq = errors.New("请求参数不合法")
)

type CreateAdminRequest struct {
	Username string
	Password string
	Nickname string
	RoleID   int64
	RoleCode string
	Status   *int16
}

type UpdateAdminRequest struct {
	Nickname *string
	RoleID   *int64
	RoleCode *string
	Status   *int16
	Password string
}

type AdminService interface {
	CreateAdmin(ctx context.Context, actorID, actorIP string, req CreateAdminRequest) (*Admin, error)
	Login(ctx context.Context, username, password string) (*Principal, string, error)
	GetAdminByID(ctx context.Context, id string) (*Admin, error)
	ListAdmins(ctx context.Context) ([]Admin, error)
	UpdateAdmin(ctx context.Context, actorID, actorIP, targetID string, req UpdateAdminRequest) (*Admin, error)
	DeleteAdmin(ctx context.Context, actorID, actorIP, targetID string) error
}

type adminService struct {
	repo AdminRepository
}

func NewAdminService(repo AdminRepository) AdminService {
	return &adminService{
		repo: repo,
	}
}

func (s *adminService) CreateAdmin(ctx context.Context, actorID, actorIP string, req CreateAdminRequest) (*Admin, error) {
	username := strings.TrimSpace(req.Username)
	nickname := strings.TrimSpace(req.Nickname)
	roleCode := strings.TrimSpace(req.RoleCode)
	if len(username) < 4 || len(username) > 50 || len(req.Password) < 6 || nickname == "" || len(nickname) > 50 {
		return nil, ErrInvalidAdminReq
	}
	if req.RoleID <= 0 && roleCode == "" {
		return nil, ErrInvalidAdminReq
	}
	status := StatusActive
	if req.Status != nil {
		if *req.Status != StatusActive && *req.Status != StatusDisabled {
			return nil, ErrInvalidAdminReq
		}
		status = *req.Status
	}

	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	return s.repo.CreateAdminTx(ctx, actorID, actorIP, CreateAdminInput{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: string(hashedBytes),
		Nickname:     nickname,
		RoleID:       req.RoleID,
		RoleCode:     roleCode,
		Status:       status,
	})
}

func (s *adminService) Login(ctx context.Context, username, password string) (*Principal, string, error) {
	admin, err := s.repo.GetAdminByUsername(ctx, strings.TrimSpace(username))
	if err != nil || admin == nil {
		return nil, "", ErrInvalidLogin
	}

	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		return nil, "", ErrInvalidLogin
	}

	if admin.Status != StatusActive || admin.RoleStatus != StatusActive {
		return nil, "", ErrSuspended
	}

	principal, err := s.repo.GetPrincipalByID(ctx, admin.ID)
	if err != nil {
		if errors.Is(err, ErrAccountInactive) {
			return nil, "", ErrSuspended
		}
		return nil, "", err
	}
	if principal == nil || !principal.IsActive() {
		return nil, "", ErrSuspended
	}

	token, err := GenerateToken(principal.ID, principal.Role)
	if err != nil {
		return nil, "", err
	}

	return principal, token, nil
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

func (s *adminService) UpdateAdmin(ctx context.Context, actorID, actorIP, targetID string, req UpdateAdminRequest) (*Admin, error) {
	if req.Nickname != nil {
		trimmed := strings.TrimSpace(*req.Nickname)
		if trimmed == "" || len(trimmed) > 50 {
			return nil, ErrInvalidAdminReq
		}
		req.Nickname = &trimmed
	}
	if req.Status != nil && *req.Status != StatusActive && *req.Status != StatusDisabled {
		return nil, ErrInvalidAdminReq
	}
	if req.RoleID != nil && *req.RoleID <= 0 {
		return nil, ErrInvalidAdminReq
	}

	var passwordHash string
	if req.Password != "" {
		if len(req.Password) < 6 {
			return nil, ErrInvalidAdminReq
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		passwordHash = string(hashed)
	}

	return s.repo.UpdateAdminTx(ctx, actorID, actorIP, targetID, UpdateAdminInput{
		Nickname:     req.Nickname,
		RoleID:       req.RoleID,
		RoleCode:     req.RoleCode,
		Status:       req.Status,
		PasswordHash: passwordHash,
	})
}

func (s *adminService) DeleteAdmin(ctx context.Context, actorID, actorIP, targetID string) error {
	if actorID == targetID {
		return ErrSelfModifyForbidden
	}
	return s.repo.DeleteAdminTx(ctx, actorID, actorIP, targetID)
}
