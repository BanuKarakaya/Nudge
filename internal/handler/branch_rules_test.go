package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"nudge/internal/coding"
	"strings"
	"testing"
)

type branchSettingsFake struct {
	calls int
	rule  coding.BranchRule
}

func (s *branchSettingsFake) BranchTemplate(context.Context, coding.Identity) (string, error) {
	return coding.DefaultBranchTemplate, nil
}
func (s *branchSettingsFake) Settings(context.Context) (coding.BranchSettings, error) {
	s.calls++
	return coding.BranchSettings{}, nil
}
func (s *branchSettingsFake) SaveRule(_ context.Context, r coding.BranchRule) error {
	s.calls++
	s.rule = r
	return nil
}
func TestDashboardBranchRuleAuthorizationAndValidation(t *testing.T) {
	for _, tc := range []struct {
		method, token, body, site string
		status, calls             int
	}{
		{"GET", "", "", "", 401, 0}, {"PUT", "wrong", `{"template":"feature/{slug}"}`, "", 401, 0},
		{"GET", "secret", "", "", 200, 1},
		{"PUT", "secret", `{"team_id":"T","slack_user_id":"U","template":"feature/{task_id}"}`, "", 200, 1},
		{"PUT", "secret", `{"team_id":"T","template":"main"}`, "", 400, 0},
		{"PUT", "secret", `{"team_id":"T","template":"feature/{task_id}"}`, "cross-site", 403, 0},
		{"PUT", "secret", `{"team_id":"T","template":""}`, "", 200, 1},
		{"PUT", "secret", `{"team_id":"T","template":"a"}{}`, "", 400, 0},
		{"POST", "secret", `{"template":"feature/{slug}"}`, "", 200, 0},
	} {
		s := &branchSettingsFake{}
		h := NewDashboardHandler(nil, "secret")
		h.WithBranchRules(s)
		mux := http.NewServeMux()
		h.RegisterRoutes(mux)
		path := "/api/dashboard/branch-rules"
		if tc.method == "POST" {
			path += "/preview"
		}
		r := httptest.NewRequest(tc.method, path, strings.NewReader(tc.body))
		r.Header.Set("X-Dashboard-Token", tc.token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status || s.calls != tc.calls {
			t.Fatalf("%+v got %d calls=%d %s", tc, w.Code, s.calls, w.Body.String())
		}
	}
	h := NewDashboardHandler(nil, "")
	w := httptest.NewRecorder()
	h.branchRulesAPI(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal("disabled dashboard exposed settings")
	}
}
