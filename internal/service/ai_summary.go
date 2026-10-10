package service

import "context"

type TextSummarizer interface {
	Summarize(ctx context.Context, input string) (string, error)
}

type RSSIntentClassifier interface {
	ClassifyRSSRequest(ctx context.Context, request string) (string, error)
}

type AssistantResponder interface {
	Respond(ctx context.Context, request string) (string, error)
}
