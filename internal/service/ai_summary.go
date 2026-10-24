package service

import "context"

// WebAssistant provides the optional external web search tool.
type WebAssistant interface {
	SearchWeb(context.Context, string) (string, error)
}
