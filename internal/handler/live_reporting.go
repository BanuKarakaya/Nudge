package handler

import (
	"context"
	"errors"
	"log"
	"nudge/internal/analytics"
	"nudge/internal/repository"
	"nudge/internal/service"
	"nudge/internal/slack"
	"nudge/internal/workspace"
	"time"
)

func (h *SlackEventHandler) reportingContext(ctx context.Context, j repository.SlackJob) context.Context {
	store, ok := h.jobs.(repository.JobReporter)
	if !ok {
		return ctx
	}
	if modelStore, ok := h.jobs.(repository.ModelCallReporter); ok {
		ctx = analytics.WithModelReporter(ctx, func(call analytics.ModelCall) {
			persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			if err := modelStore.ReportModelCall(persist, j, call); err != nil {
				log.Printf("slack_job=%d stage=model_trace_unavailable", j.ID)
			}
		})
	}
	return analytics.WithReporter(ctx, func(e analytics.LiveEvent) {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 750*time.Millisecond)
		defer cancel()
		if err := store.ReportJob(persist, j, e); err != nil {
			log.Printf("slack_job=%d stage=telemetry_unavailable", j.ID)
		}
	})
}
func liveError(err error) string {
	if errors.Is(err, repository.ErrWorkspaceNotInstalled) {
		return "workspace_not_installed"
	}
	var slackError *slack.APIError
	if errors.As(err, &slackError) {
		return "slack_" + slackError.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "interrupted"
	}
	if errors.Is(err, workspace.ErrAINotConfigured) {
		return "ai_not_configured"
	}
	if errors.Is(err, workspace.ErrDenied) {
		return "access_denied"
	}
	if errors.Is(err, repository.ErrSlackJobLeaseLost) {
		return "lease_lost"
	}
	var coded *analytics.OperationalError
	if errors.As(err, &coded) {
		return coded.Code
	}
	code, _ := service.AgentToolFailure(err)
	if code == "tool_failed" {
		return "processing_failed"
	}
	return code
}

func (h *SlackEventHandler) refreshProfile(ctx context.Context, user int64, token, slackUser string) {
	store, ok := h.installations.(interface {
		ClaimProfileRefresh(context.Context, int64) (bool, error)
		SaveProfileName(context.Context, int64, string) error
	})
	if !ok {
		return
	}
	client, ok := h.client.(interface {
		UserDisplayName(context.Context, string, string) (string, error)
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	refresh, err := store.ClaimProfileRefresh(ctx, user)
	if err != nil || !refresh {
		return
	}
	name, err := client.UserDisplayName(ctx, token, slackUser)
	if err == nil {
		_ = store.SaveProfileName(ctx, user, name)
	}
}
