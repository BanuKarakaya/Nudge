package service

import (
	"context"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

type FeedbinClient interface {
	ValidateCredentials(ctx context.Context, email, password string) error
}

type FeedbinService struct {
	connections repository.FeedbinConnectionRepository
	client      FeedbinClient
}

func NewFeedbinService(connections repository.FeedbinConnectionRepository, client FeedbinClient) *FeedbinService {
	return &FeedbinService{connections: connections, client: client}
}

func (s *FeedbinService) Connect(ctx context.Context, userID int64, email, password string) error {
	if err := s.client.ValidateCredentials(ctx, email, password); err != nil {
		return err
	}
	return s.connections.Save(ctx, domain.FeedbinConnection{
		UserID:   userID,
		Email:    email,
		Password: password,
	})
}
