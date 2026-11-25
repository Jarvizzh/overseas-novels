package auth

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type RoleHandler struct {
	service RoleService
}

func NewRoleHandler(service RoleService) *RoleHandler {
	return &RoleHandler{service: service}
}

func (h *RoleHandler) RegisterRoutes(rg *gin.RouterGroup) {
	roles := rg.Group("/auth/roles", AuthMiddleware())
	{
		roles.GET("", RequireAnyPermission(PermSystemRolesRead, PermSystemAccountsRead), h.ListRoles)
		roles.POST("", RequireSuperAdmin(), h.CreateRole)
		roles.PUT("/:id", RequireSuperAdmin(), h.UpdateRole)
		roles.DELETE("/:id", RequireSuperAdmin(), h.DeleteRole)
	}
}

func (h *RoleHandler) ListRoles(c *gin.Context) {
	roles, err := h.service.ListRoles(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if roles == nil {
		roles = make([]Role, 0)
	}
	c.JSON(http.StatusOK, roles)
}

type CreateRoleHTTPReq struct {
	Code        string   `json:"code" binding:"required"`
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	Status      *int16   `json:"status"`
	Permissions []string `json:"permissions"`
}

func (h *RoleHandler) CreateRole(c *gin.Context) {
	principal, ok := PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	var req CreateRoleHTTPReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	role, err := h.service.CreateRole(c.Request.Context(), principal.ID, c.ClientIP(), CreateRoleRequest{
		Code:        req.Code,
		Name:        req.Name,
		Description: req.Description,
		Status:      req.Status,
		Permissions: req.Permissions,
	})
	if err != nil {
		writeRoleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, role)
}

type UpdateRoleHTTPReq struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Status      *int16    `json:"status"`
	Permissions *[]string `json:"permissions"`
}

func (h *RoleHandler) UpdateRole(c *gin.Context) {
	principal, ok := PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role id"})
		return
	}

	var req UpdateRoleHTTPReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	role, err := h.service.UpdateRole(c.Request.Context(), principal.ID, c.ClientIP(), id, UpdateRoleRequest{
		Name:        req.Name,
		Description: req.Description,
		Status:      req.Status,
		Permissions: req.Permissions,
	})
	if err != nil {
		writeRoleError(c, err)
		return
	}
	c.JSON(http.StatusOK, role)
}

func (h *RoleHandler) DeleteRole(c *gin.Context) {
	principal, ok := PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role id"})
		return
	}

	if err := h.service.DeleteRole(c.Request.Context(), principal.ID, c.ClientIP(), id); err != nil {
		writeRoleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Role deleted successfully"})
}

func writeRoleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrAccountInactive):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error(), "code": "account_inactive"})
	case errors.Is(err, ErrSuperAdminRequired):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "super_admin_required"})
	case errors.Is(err, ErrPermissionDenied):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "permission_denied"})
	case errors.Is(err, ErrRoleNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrRoleCodeExists), errors.Is(err, ErrRoleInUse):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrSuperAdminOnlyPermission), errors.Is(err, ErrInvalidPermission),
		errors.Is(err, ErrSystemRoleImmutable), errors.Is(err, ErrSystemRoleDeleteForbidden),
		errors.Is(err, ErrInvalidRoleInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
