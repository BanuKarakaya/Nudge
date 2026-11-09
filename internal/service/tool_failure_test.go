package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"nudge/internal/coding"
)

func TestToolFailuresExposeOnlySafeCategories(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{coding.ErrMergeRequest, "merge_intent_required"},
		{coding.ErrMergeIntentUnavailable, "merge_intent_unavailable"},
		{coding.ErrBranchMerge, "branch_merge_intent_required"},
		{coding.ErrChangeMoved, "merge_revision_changed"},
		{coding.ErrMergeBlocked, "merge_blocked"},
		{coding.ErrAPIScope, "gitlab_api_scope_required"},
		{errAgentArguments, "invalid_arguments"},
		{coding.ErrArguments, "invalid_arguments"},
		{errors.New("secret-token-private-diagnostics"), "tool_failed"},
	} {
		code, message := AgentToolFailure(fmt.Errorf("secret-token-private-diagnostics: %w", tc.err))
		if code != tc.code || message == "" || strings.Contains(message, "secret-token") {
			t.Fatal(code, message)
		}
	}
}
