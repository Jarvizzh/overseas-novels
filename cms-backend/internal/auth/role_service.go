package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var (
	ErrInvalidRoleInput = errors.New("角色参数不合法")
	roleCodePattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,63}$`)
)

type CreateRoleRequest struct {
	Code        string
	Name        string
	Description string
	Status      *int16
	Permissions []string
}

type UpdateRoleRequest struct {
	Name        *string
	Description *string
	Status      *int16
	Permissions *[]string
}

type RoleService interface {
	ListRoles(ctx context.Context) ([]Role, error)
	CreateRole(ctx context.Context, actorID, actorIP string, req CreateRoleRequest) (*Role, error)
	UpdateRole(ctx context.Context, actorID, actorIP string, id int64, req UpdateRoleRequest) (*Role, error)
	DeleteRole(ctx context.Context, actorID, actorIP string, id int64) error
}

type roleService struct {
	repo RoleRepository
}

func NewRoleService(repo RoleRepository) RoleService {
	return &roleService{repo: repo}
}

func (s *roleService) ListRoles(ctx context.Context) ([]Role, error) {
	return s.repo.ListRoles(ctx)
}

func (s *roleService) CreateRole(ctx context.Context, actorID, actorIP string, req CreateRoleRequest) (*Role, error) {
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	desc := strings.TrimSpace(req.Description)
	if !roleCodePattern.MatchString(code) || name == "" || len(name) > 100 {
		return nil, ErrInvalidRoleInput
	}

	status := StatusActive
	if req.Status != nil {
		if *req.Status != StatusActive && *req.Status != StatusDisabled {
			return nil, ErrInvalidRoleInput
		}
		status = *req.Status
	}

	perms, err := ValidateAssignablePermissions(req.Permissions)
	if err != nil {
		return nil, err
	}

	return s.repo.CreateRoleTx(ctx, actorID, actorIP, CreateRoleInput{
		Code:        code,
		Name:        name,
		Description: desc,
		Status:      status,
		Permissions: perms,
	})
}

func (s *roleService) UpdateRole(ctx context.Context, actorID, actorIP string, id int64, req UpdateRoleRequest) (*Role, error) {
	if id <= 0 {
		return nil, ErrInvalidRoleInput
	}
	var namePtr *string
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 100 {
			return nil, ErrInvalidRoleInput
		}
		namePtr = &name
	}
	var descPtr *string
	if req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		descPtr = &desc
	}
	if req.Status != nil && *req.Status != StatusActive && *req.Status != StatusDisabled {
		return nil, ErrInvalidRoleInput
	}

	var permsPtr *[]string
	if req.Permissions != nil {
		perms, err := ValidateAssignablePermissions(*req.Permissions)
		if err != nil {
			return nil, err
		}
		permsPtr = &perms
	}

	return s.repo.UpdateRoleTx(ctx, actorID, actorIP, id, UpdateRoleInput{
		Name:        namePtr,
		Description: descPtr,
		Status:      req.Status,
		Permissions: permsPtr,
	})
}

func (s *roleService) DeleteRole(ctx context.Context, actorID, actorIP string, id int64) error {
	if id <= 0 {
		return ErrInvalidRoleInput
	}
	return s.repo.DeleteRoleTx(ctx, actorID, actorIP, id)
}
