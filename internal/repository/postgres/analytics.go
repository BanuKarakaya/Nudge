package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/analytics"
	"nudge/internal/repository"
	"nudge/migrations"
)

var _ repository.AnalyticsRepository = (*AnalyticsRepository)(nil)

type AnalyticsRepository struct {
	db *pgxpool.Pool
}

func NewAnalyticsRepository(ctx context.Context, db *pgxpool.Pool) (*AnalyticsRepository, error) {
	if _, err := db.Exec(ctx, migrations.AnalyticsSchema); err != nil {
		return nil, err
	}
	return &AnalyticsRepository{db: db}, nil
}

func (r *AnalyticsRepository) RecordInteraction(ctx context.Context, record repository.InteractionRecord) error {
	models, providers, prompt, completion, total := aggregateUsage(record.Usage)
	if record.Status == "" {
		record.Status = "processing"
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now()
	}
	operations := record.Operations
	if operations == nil {
		operations = []analytics.Operation{}
	}
	encodedOperations, err := json.Marshal(operations)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO nudge_ai_interactions
			(request_id, team_id, channel_id, thread_ts, slack_user_id, user_id,
			 request_text, response_text, action, status, provider, model, latency_ms,
			 prompt_tokens, completion_tokens, total_tokens, created_at, operations, trace_version)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6, 0),$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18::jsonb,1)
		ON CONFLICT (request_id) DO UPDATE SET
			response_text=EXCLUDED.response_text, action=EXCLUDED.action,
			status=EXCLUDED.status, provider=EXCLUDED.provider, model=EXCLUDED.model,
			latency_ms=EXCLUDED.latency_ms, prompt_tokens=EXCLUDED.prompt_tokens,
			completion_tokens=EXCLUDED.completion_tokens, total_tokens=EXCLUDED.total_tokens,
			operations=EXCLUDED.operations, trace_version=EXCLUDED.trace_version`,
		record.RequestID, record.TeamID, record.ChannelID, record.ThreadTS, record.SlackUserID,
		record.UserID, record.RequestText, record.ResponseText, record.Action, record.Status,
		strings.Join(providers, " → "), strings.Join(models, " → "), record.LatencyMS,
		prompt, completion, total, record.StartedAt, encodedOperations)
	return err
}

func aggregateUsage(calls []analytics.AIUsage) (models, providers []string, prompt, completion, total int64) {
	seenModels := make(map[string]bool)
	seenProviders := make(map[string]bool)
	for _, call := range calls {
		if call.Model != "" && !seenModels[call.Model] {
			models = append(models, call.Model)
			seenModels[call.Model] = true
		}
		if call.Provider != "" && !seenProviders[call.Provider] {
			providers = append(providers, call.Provider)
			seenProviders[call.Provider] = true
		}
		prompt += int64(call.PromptTokens)
		completion += int64(call.CompletionTokens)
		total += int64(call.TotalTokens)
	}
	return models, providers, prompt, completion, total
}

func (r *AnalyticsRepository) Dashboard(ctx context.Context, filters repository.DashboardFilters) (repository.DashboardSummary, error) {
	if filters.Limit <= 0 || filters.Limit > 100 {
		filters.Limit = 24
	}
	if filters.Offset < 0 {
		filters.Offset = 0
	}
	if filters.To.IsZero() {
		filters.To = time.Now()
	}
	if filters.From.IsZero() {
		filters.From = filters.To.Add(-7 * 24 * time.Hour)
	}

	where := "created_at >= $1 AND created_at < $2"
	args := []any{filters.From, filters.To}
	if filters.Model != "" {
		where += fmt.Sprintf(" AND model ILIKE $%d", len(args)+1)
		args = append(args, "%"+filters.Model+"%")
	}
	if filters.Status == "issues" {
		where += " AND status IN ('failed', 'degraded')"
	} else if filters.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", len(args)+1)
		args = append(args, filters.Status)
	}

	var result repository.DashboardSummary
	if err := r.db.QueryRow(ctx, `SELECT
		COUNT(*), COUNT(*) FILTER (WHERE status = 'success'), COUNT(*) FILTER (WHERE status IN ('failed', 'degraded')),
		COUNT(DISTINCT NULLIF(slack_user_id, '')), COALESCE(AVG(latency_ms), 0),
		COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(total_tokens), 0)
		FROM nudge_ai_interactions WHERE `+where, args...).Scan(
		&result.TotalMessages, &result.SuccessfulMessages, &result.ErrorCount, &result.ActiveUsers,
		&result.AverageLatencyMS, &result.PromptTokens, &result.CompletionTokens, &result.TotalTokens); err != nil {
		return result, err
	}

	rows, err := r.db.Query(ctx, `SELECT TO_CHAR(created_at AT TIME ZONE 'Europe/Istanbul', 'YYYY-MM-DD'), COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(AVG(latency_ms), 0)
		FROM nudge_ai_interactions WHERE `+where+` GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var day repository.DailyUsage
		if err := rows.Scan(&day.Date, &day.Messages, &day.Tokens, &day.AverageLatency); err != nil {
			rows.Close()
			return result, err
		}
		result.Daily = append(result.Daily, day)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	rows, err = r.db.Query(ctx, `SELECT model, provider, COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(AVG(latency_ms), 0)
		FROM nudge_ai_interactions WHERE `+where+` GROUP BY model, provider ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var model repository.ModelUsage
		if err := rows.Scan(&model.Model, &model.Provider, &model.Messages, &model.Tokens, &model.AverageLatency); err != nil {
			rows.Close()
			return result, err
		}
		result.Models = append(result.Models, model)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	argsRecent := append([]any{}, args...)
	argsRecent = append(argsRecent, filters.Limit, filters.Offset)
	rows, err = r.db.Query(ctx, `SELECT id, request_text, response_text, action, status, model, provider, latency_ms, prompt_tokens, completion_tokens, total_tokens, created_at, operations, trace_version, request_id
		FROM nudge_ai_interactions WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT $`+fmt.Sprint(len(argsRecent)-1)+` OFFSET $`+fmt.Sprint(len(argsRecent)), argsRecent...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item repository.Interaction
		var rawOperations []byte
		if err := rows.Scan(&item.ID, &item.Request, &item.Response, &item.Action, &item.Status, &item.Model, &item.Provider, &item.LatencyMS, &item.PromptTokens, &item.CompletionTokens, &item.TotalTokens, &item.CreatedAt, &rawOperations, &item.TraceVersion, &item.RequestID); err != nil {
			rows.Close()
			return result, err
		}
		if err := json.Unmarshal(rawOperations, &item.Operations); err != nil {
			rows.Close()
			return result, err
		}
		result.Recent = append(result.Recent, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	return result, nil
}
