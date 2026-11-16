package service

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"

	"nudge/internal/domain"
	"nudge/internal/raindrop"
	"nudge/internal/repository"
)

type RaindropOAuthClient interface {
	ExchangeCode(ctx context.Context, code string) (raindrop.TokenResponse, error)
	GetAuthenticatedUser(ctx context.Context, accessToken string) (int64, error)
}

type RaindropService struct {
	connections repository.RaindropConnectionRepository
	client      RaindropOAuthClient
}

func NewRaindropService(connections repository.RaindropConnectionRepository, client RaindropOAuthClient) *RaindropService {
	return &RaindropService{connections: connections, client: client}
}

func (s *RaindropService) Connect(ctx context.Context, userID int64, code string) error {
	tokens, err := s.client.ExchangeCode(ctx, code)
	if err != nil {
		return err
	}
	raindropUserID, err := s.client.GetAuthenticatedUser(ctx, tokens.AccessToken)
	if err != nil {
		return err
	}
	return s.connections.Save(ctx, domain.RaindropConnection{
		UserID:         userID,
		RaindropUserID: &raindropUserID,
		AccessToken:    tokens.AccessToken,
		RefreshToken:   tokens.RefreshToken,
		ExpiresAt:      raindrop.ExpiresAt(tokens.ExpiresIn),
	})
}

func (s *RaindropService) IsConnected(ctx context.Context, userID int64) (bool, error) {
	_, err := s.connections.GetByUserID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
