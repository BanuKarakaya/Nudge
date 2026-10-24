package service

import "context"

// AgentRuntime owns planning, tool selection, context management and generation.
// The application owns identity, tool authorization, storage and delivery.
type AgentRuntime interface {
	Run(context.Context, AgentRequest) (AgentResult, error)
}

type AgentRequest struct {
	Key      ConversationKey
	ThreadID string
	Input    string
	History  ConversationState
	Tools    AgentTools
}

type AgentResult struct {
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
