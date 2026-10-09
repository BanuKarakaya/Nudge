package service

import "context"

type TextSummarizer interface {
	Summarize(ctx context.Context, input string) (string, error)
}
