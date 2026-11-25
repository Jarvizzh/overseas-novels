package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	service AdminService
}

func NewAuthHandler(service AdminService) *AuthHandler {
	return &AuthHandler{
		service: service,
	}
}

func (h *AuthHandler) RegisterRoutes(rg *gin.RouterGroup) {
	authGroup := rg.Group("/auth")
	{
		authGroup.POST("/login", h.Login)
		authGroup.GET("/me", AuthMiddleware(), h.Me)
		authGroup.GET("/permissions", AuthMiddleware(), RequireAnyPermission(PermSystemRolesRead, PermSystemAccountsRead), h.Permissions)
		authGroup.GET("/admins", AuthMiddleware(), RequirePermission(PermSystemAccountsRead), h.ListAdmins)
		authGroup.POST("/admins", AuthMiddleware(), RequireSuperAdmin(), h.CreateAdmin)
		authGroup.POST("/register", AuthMiddleware(), RequireSuperAdmin(), h.CreateAdmin)
		authGroup.PUT("/admins/:id", AuthMiddleware(), RequireSuperAdmin(), h.UpdateAdmin)
		authGroup.DELETE("/admins/:id", AuthMiddleware(), RequireSuperAdmin(), h.DeleteAdmin)
	}
}

type CreateAdminHTTPReq struct {
	Username string `json:"username" binding:"required,min=4,max=50"`
	Password string `json:"password" binding:"required,min=6"`
	Nickname string `json:"nickname" binding:"required,max=50"`
	RoleID   int64  `json:"role_id"`
	Role     string `json:"role"`
	Status   *int16 `json:"status"`
}

func (h *AuthHandler) CreateAdmin(c *gin.Context) {
	principal, ok := PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	var req CreateAdminHTTPReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RoleID <= 0 && req.Role == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role_id is required"})
		return
	}

	admin, err := h.service.CreateAdmin(c.Request.Context(), principal.ID, c.ClientIP(), CreateAdminRequest{
		Username: req.Username,
		Password: req.Password,
		Nickname: req.Nickname,
		RoleID:   req.RoleID,
		RoleCode: req.Role,
		Status:   req.Status,
	})
	if err != nil {
		writeAdminError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Admin user registered successfully",
		"admin":   admin,
	})
}

type LoginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	principal, token, err := h.service.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidLogin) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrSuspended) {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "account_inactive"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Login failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"admin": principal,
	})
}

func (h *AuthHandler) Me(c *gin.Context) {
	principal, ok := PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	c.JSON(http.StatusOK, principal)
}

func (h *AuthHandler) Permissions(c *gin.Context) {
	c.JSON(http.StatusOK, PermissionCatalog())
}

func (h *AuthHandler) ListAdmins(c *gin.Context) {
	admins, err := h.service.ListAdmins(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if admins == nil {
		admins = make([]Admin, 0)
	}
	c.JSON(http.StatusOK, admins)
}

type UpdateAdminHTTPReq struct {
	Nickname *string `json:"nickname"`
	RoleID   *int64  `json:"role_id"`
	Role     *string `json:"role"`
	Status   *int16  `json:"status"`
	Password string  `json:"password"`
}

func (h *AuthHandler) UpdateAdmin(c *gin.Context) {
	principal, ok := PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	id := c.Param("id")
	var req UpdateAdminHTTPReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.service.UpdateAdmin(c.Request.Context(), principal.ID, c.ClientIP(), id, UpdateAdminRequest{
		Nickname: req.Nickname,
		RoleID:   req.RoleID,
		RoleCode: req.Role,
		Status:   req.Status,
		Password: req.Password,
	})
	if err != nil {
		writeAdminError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Admin updated successfully",
		"admin":   updated,
	})
}

func (h *AuthHandler) DeleteAdmin(c *gin.Context) {
	principal, ok := PrincipalFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	id := c.Param("id")
	if err := h.service.DeleteAdmin(c.Request.Context(), principal.ID, c.ClientIP(), id); err != nil {
		writeAdminError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Admin deleted successfully"})
}

func writeAdminError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrAccountInactive):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error(), "code": "account_inactive"})
	case errors.Is(err, ErrSuperAdminRequired):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "super_admin_required"})
	case errors.Is(err, ErrPermissionDenied):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "permission_denied"})
	case errors.Is(err, ErrUsernameExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "username_exists"})
	case errors.Is(err, ErrAdminNotFound), errors.Is(err, ErrRoleNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrSelfModifyForbidden), errors.Is(err, ErrLastSuperAdmin),
		errors.Is(err, ErrRoleInactive), errors.Is(err, ErrInvalidAdminReq):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
