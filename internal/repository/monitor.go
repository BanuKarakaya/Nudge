package repository

import (
	"context"
	"nudge/internal/analytics"
	"time"
)

type JobReporter interface {
	ReportJob(context.Context, SlackJob, analytics.LiveEvent) error
}
type MonitorFilters struct {
	From, To            time.Time
	Status, Model, User string
	JobID               int64
	Offset              int
}
type MonitorEvent struct {
	analytics.LiveEvent
	At      time.Time `json:"at"`
	Attempt int       `json:"attempt"`
}
type MonitorRequest struct {
	RequestText, ResponseText                                          string
	ContentVisible                                                     bool
	ModelCalls                                                         []analytics.ModelCall
	CachedTokens, ReasoningTokens, ModelCallCount, CostCalls           int64
	CostUSD                                                            float64
	ToolFailures                                                       int64
	ID                                                                 int64
	User, Name, Channel, Thread, Status, Stage, Model, Tool, ErrorCode string
	Attempts, TraceCount                                               int
	CreatedAt, UpdatedAt                                               time.Time
	AvailableAt, LeaseUntil, FinishedAt, DeliveredAt                   *time.Time
	ResponseReady, Stalled                                             bool
	LatencyMS, PromptTokens, CompletionTokens, TotalTokens             int64
	Events                                                             []MonitorEvent
}
type MonitorUser struct {
	Name                     string
	User                     string
	Requests, Tokens, Errors int64
	LastSeen                 time.Time
}
type MonitorModel struct {
	Model, Provider                                         string
	Requests, Calls, PromptTokens, CompletionTokens, Tokens int64
}
type MonitorSummary struct {
	HTTPErrors                                                                                                                       []HTTPError
	CachedTokens, ReasoningTokens, ModelCallCount, CostCalls                                                                         int64
	CostUSD                                                                                                                          float64
	IssueRequests, ToolIssueRequests                                                                                                 int64
	Requests, Queued, Running, Completed, Failed, Undelivered, ActiveUsers, Tokens, PromptTokens, CompletionTokens, FilteredRequests int64
	AverageMS, P95MS                                                                                                                 float64
	Daily                                                                                                                            []DailyUsage
	Models                                                                                                                           []MonitorModel
	Users                                                                                                                            []MonitorUser
	Recent                                                                                                                           []MonitorRequest
	UpdatedAt                                                                                                                        time.Time
}
type MonitorRepository interface {
	Monitor(context.Context, int64, bool, MonitorFilters) (MonitorSummary, error)
}

// ModelCallReporter stores encrypted content separately from operational events.
type ModelCallReporter interface {
	ReportModelCall(context.Context, SlackJob, analytics.ModelCall) error
}

type HTTPError struct {
	RequestID, Method, Route string
	Status                   int
	DurationMS               int64
	CreatedAt                time.Time
}
type HTTPErrorRecorder interface {
	RecordHTTPError(context.Context, int64, HTTPError) error
}
