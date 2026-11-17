package slack

import (
	"encoding/json"
	"testing"
)

func TestSlackMemberBotJSONAndPermanentErrors(t *testing.T) {
	var response usersListResponse
	if json.Unmarshal([]byte(`{"ok":true,"members":[{"id":"B","is_bot":true},{"id":"U","deleted":true}]}`), &response) != nil || !response.Members[0].IsBot || !response.Members[1].Deleted {
		t.Fatal("Slack bot flags not decoded")
	}
	for _, code := range []string{"cant_dm_bot", "token_revoked", "user_not_found", "missing_scope"} {
		if !(&APIError{Code: code, Status: 200}).Permanent() {
			t.Fatal("permanent error retried", code)
		}
	}
	for _, status := range []int{429, 500, 503} {
		if (&APIError{Code: "cant_dm_bot", Status: status}).Permanent() {
			t.Fatal("outage classified permanent")
		}
	}
}
