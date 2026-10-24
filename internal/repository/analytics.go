package repository

import (
	"context"
	"time"

	"nudge/internal/analytics"
)

type InteractionRecord struct {
	RequestID    string
	TeamID       string
	ChannelID    string
	ThreadTS     string
	SlackUserID  string
	UserID       int64
	RequestText  string
	ResponseText string
	Action       string
	Status       string
	LatencyMS    int64
	StartedAt    time.Time
	Usage        []analytics.AIUsage
	Operations   []analytics.Operation
}

type DashboardFilters struct {
	From   time.Time
	To     time.Time
	Model  string
	Status string
	Limit  int
	Offset int
}

type DashboardSummary struct {
	TotalMessages      int64         `json:"total_messages"`
	SuccessfulMessages int64         `json:"successful_messages"`
	ErrorCount         int64         `json:"error_count"`
	ActiveUsers        int64         `json:"active_users"`
	AverageLatencyMS   float64       `json:"average_latency_ms"`
	PromptTokens       int64         `json:"prompt_tokens"`
	CompletionTokens   int64         `json:"completion_tokens"`
	TotalTokens        int64         `json:"total_tokens"`
	Daily              []DailyUsage  `json:"daily"`
	Models             []ModelUsage  `json:"models"`
	Recent             []Interaction `json:"recent"`
}

type DailyUsage struct {
	Date           string  `json:"date"`
	Messages       int64   `json:"messages"`
	Tokens         int64   `json:"tokens"`
	AverageLatency float64 `json:"average_latency_ms"`
}

type ModelUsage struct {
	Model          string  `json:"model"`
	Provider       string  `json:"provider"`
	Messages       int64   `json:"messages"`
	Tokens         int64   `json:"tokens"`
	AverageLatency float64 `json:"average_latency_ms"`
}

type Interaction struct {
	RequestID        string                `json:"request_id"`
	TraceVersion     int                   `json:"trace_version"`
	Operations       []analytics.Operation `json:"operations"`
	ID               int64                 `json:"id"`
	Request          string                `json:"request"`
	Response         string                `json:"response"`
	Action           string                `json:"action"`
	Status           string                `json:"status"`
	Model            string                `json:"model"`
	Provider         string                `json:"provider"`
	LatencyMS        int64                 `json:"latency_ms"`
	PromptTokens     int64                 `json:"prompt_tokens"`
	CompletionTokens int64                 `json:"completion_tokens"`
	TotalTokens      int64                 `json:"total_tokens"`
	CreatedAt        time.Time             `json:"created_at"`
}

type AnalyticsRepository interface {
	RecordInteraction(context.Context, InteractionRecord) error
	Dashboard(context.Context, DashboardFilters) (DashboardSummary, error)
}
