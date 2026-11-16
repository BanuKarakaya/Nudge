package service

import "context"

// AgentRuntime owns planning, tool selection, context management and generation.
// The application owns identity, tool authorization, storage and delivery.
type AgentRuntime interface {
	Run(context.Context, AgentRequest) (AgentResult, error)
}

type AgentRequest struct {
	UserID   int64
	Key      ConversationKey
	ThreadID string
	Input    string
	History  ConversationState
	Tools    AgentTools
}

type AgentResult struct {
	AIRevision string
	ThreadID   string
	Text       string
	TokenUsage *AgentTokenUsage
}

// Cumulative native-thread counters; only used for usage accounting, not routing.
type AgentTokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Total  int `json:"total"`
}
