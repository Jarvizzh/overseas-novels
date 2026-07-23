package domain

import (
	"time"
)

type SystemDomain struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Domain    string    `json:"domain"`
	Type      string    `json:"type"`       // "main" or "sub"
	Status    int16     `json:"status"`     // 1-Enabled, 2-Disabled
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateDomainRequest struct {
	Name      string `json:"name" binding:"required"`
	Domain    string `json:"domain" binding:"required"`
	Type      string `json:"type" binding:"required,oneof=main sub"`
	IsDefault bool   `json:"is_default"`
}

type UpdateDomainStatusRequest struct {
	Status int16 `json:"status" binding:"required,oneof=1 2"`
}
