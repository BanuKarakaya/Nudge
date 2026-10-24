package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"nudge/internal/repository"
)

type paginatedDashboardRepo struct {
	repository.AnalyticsRepository
	filters repository.DashboardFilters
	calls   int
}

func (r *paginatedDashboardRepo) Dashboard(_ context.Context, filters repository.DashboardFilters) (repository.DashboardSummary, error) {
	r.filters = filters
	r.calls++
	return repository.DashboardSummary{TotalMessages: 250}, nil
}

func TestDashboardPaginationAndFilters(t *testing.T) {
	for _, tc := range []struct {
		query               string
		code, limit, offset int
	}{
		{"", 200, 24, 0},
		{"?limit=100&offset=200&range=30d&model=gpt&status=issues", 200, 100, 200},
		{"?limit=0", 400, 0, 0},
		{"?limit=101", 400, 0, 0},
		{"?offset=-1", 400, 0, 0},
		{"?offset=oops", 400, 0, 0},
		{"?limit=99999999999999999999", 400, 0, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			repo := &paginatedDashboardRepo{}
			h := NewDashboardHandler(repo, "test-token")
			r := httptest.NewRequest("GET", "/api/dashboard"+tc.query, nil)
			r.Header.Set("X-Dashboard-Token", "test-token")
			w := httptest.NewRecorder()
			h.api(w, r)
			if w.Code != tc.code {
				t.Fatalf("status=%d want=%d", w.Code, tc.code)
			}
			if tc.code != 200 {
				if repo.calls != 0 {
					t.Fatal("invalid pagination reached repository")
				}
				return
			}
			if repo.filters.Limit != tc.limit || repo.filters.Offset != tc.offset {
				t.Fatalf("filters=%+v", repo.filters)
			}
			if tc.offset > 0 && (repo.filters.Model != "gpt" || repo.filters.Status != "issues" || repo.filters.To.Sub(repo.filters.From).Hours() != 720) {
				t.Fatalf("filters not preserved: %+v", repo.filters)
			}
		})
	}
}
