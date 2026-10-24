package service

import (
	"context"
	"time"

	"nudge/internal/feedbin"
	"nudge/internal/repository"
)

type FeedbinDigestClient interface {
	ListUnreadSince(ctx context.Context, email, password string, since time.Time) ([]feedbin.Entry, error)
	ListTaggings(ctx context.Context, email, password string) ([]feedbin.Tagging, error)
}

type FeedbinDigestService struct {
	connections repository.FeedbinConnectionRepository
	client      FeedbinDigestClient
}

func NewFeedbinDigestService(connections repository.FeedbinConnectionRepository, client FeedbinDigestClient) *FeedbinDigestService {
	return &FeedbinDigestService{connections: connections, client: client}
}

const SummaryUnavailable = "RSS özetini şu anda tamamlayamadım. Biraz sonra tekrar deneyebilir misin?"
