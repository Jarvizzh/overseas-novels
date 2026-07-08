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
		authGroup.POST("/register", h.Register) // Used for seeding and testing
		authGroup.GET("/me", AuthMiddleware(), h.Me)
		authGroup.GET("/admins", AuthMiddleware(), RoleMiddleware("SuperAdmin"), h.ListAdmins)
		authGroup.PUT("/admins/:id", AuthMiddleware(), RoleMiddleware("SuperAdmin"), h.UpdateAdmin)
		authGroup.DELETE("/admins/:id", AuthMiddleware(), RoleMiddleware("SuperAdmin"), h.DeleteAdmin)
	}
}

type RegisterReq struct {
	Username string `json:"username" binding:"required,min=4"`
	Password string `json:"password" binding:"required,min=6"`
	Nickname string `json:"nickname" binding:"required"`
	Role     string `json:"role" binding:"required,oneof=SuperAdmin Editor Support MediaBuyer Finance"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	admin, err := h.service.Register(ctx, req.Username, req.Password, req.Nickname, req.Role)
	if err != nil {
		if errors.Is(err, ErrUsernameExists) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Admin user registered successfully",
		"admin": gin.H{
			"id":         admin.ID,
			"username":   admin.Username,
			"nickname":   admin.Nickname,
			"role":       admin.Role,
			"created_at": admin.CreatedAt,
		},
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

	ctx := c.Request.Context()
	admin, token, err := h.service.Login(ctx, req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidLogin) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrSuspended) {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Login failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"admin": gin.H{
			"id":       admin.ID,
			"username": admin.Username,
			"nickname": admin.Nickname,
			"role":     admin.Role,
		},
	})
}

func (h *AuthHandler) Me(c *gin.Context) {
	adminID, _ := c.Get("admin_id")
	role, _ := c.Get("admin_role")
	println("[DEBUG] Me handler called. adminID =", adminID, "role =", role)

	ctx := c.Request.Context()
	admin, err := h.service.GetAdminByID(ctx, adminID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Admin profile not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":       admin.ID,
		"username": admin.Username,
		"nickname": admin.Nickname,
		"role":     role,
	})
}

type AdminResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	Role      string    `json:"role"`
	Status    int16     `json:"status"`
	CreatedAt string    `json:"created_at"`
}

func (h *AuthHandler) ListAdmins(c *gin.Context) {
	ctx := c.Request.Context()
	admins, err := h.service.ListAdmins(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, admins)
}

type UpdateAdminReq struct {
	Nickname string `json:"nickname" binding:"required"`
	Role     string `json:"role" binding:"required,oneof=SuperAdmin Editor Support MediaBuyer Finance"`
	Password string `json:"password"`
}

func (h *AuthHandler) UpdateAdmin(c *gin.Context) {
	id := c.Param("id")
	var req UpdateAdminReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.service.UpdateAdmin(ctx, id, req.Nickname, req.Role, req.Password)
	if err != nil {
		if errors.Is(err, ErrAdminNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Admin updated successfully"})
}

func (h *AuthHandler) DeleteAdmin(c *gin.Context) {
	id := c.Param("id")
	callerID, _ := c.Get("admin_id")
	if callerID == id {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot delete your own account"})
		return
	}

	ctx := c.Request.Context()
	err := h.service.DeleteAdmin(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Admin deleted successfully"})
}
