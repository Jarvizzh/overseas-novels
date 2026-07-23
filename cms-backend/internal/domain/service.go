package domain

import (
	"context"
)

type Service interface {
	ListDomains(ctx context.Context) ([]*SystemDomain, error)
	CreateDomain(ctx context.Context, req *CreateDomainRequest) (*SystemDomain, error)
	UpdateDomainStatus(ctx context.Context, id int, status int16) error
	SetDefaultDomain(ctx context.Context, id int) error
	DeleteDomain(ctx context.Context, id int) error
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{
		repo: repo,
	}
}

func (s *service) ListDomains(ctx context.Context) ([]*SystemDomain, error) {
	return s.repo.ListDomains(ctx)
}

func (s *service) CreateDomain(ctx context.Context, req *CreateDomainRequest) (*SystemDomain, error) {
	return s.repo.CreateDomain(ctx, req.Name, req.Domain, req.Type, req.IsDefault)
}

func (s *service) UpdateDomainStatus(ctx context.Context, id int, status int16) error {
	return s.repo.UpdateDomainStatus(ctx, id, status)
}

func (s *service) SetDefaultDomain(ctx context.Context, id int) error {
	return s.repo.SetDefaultDomain(ctx, id)
}

func (s *service) DeleteDomain(ctx context.Context, id int) error {
	return s.repo.DeleteDomain(ctx, id)
}
