package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testBoard = "aaaaaaaaaaaaaaaaaaaaaaaa"
const testCard = "bbbbbbbbbbbbbbbbbbbbbbbb"
const testDoing = "cccccccccccccccccccccccc"
const testTesting = "dddddddddddddddddddddddd"

func TestTrelloCreateAndMoveBoundaries(t *testing.T) {
	for _, scenario := range []string{"create", "move", "wrong-list", "wrong-board", "changed", "archived", "already", "lost-response"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			c := testClient("trello", func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" {
					writes++
					var body map[string]any
					if json.NewDecoder(req.Body).Decode(&body) != nil {
						t.Fatal("invalid json")
					}
					if req.Header.Get("Authorization") != "Bearer token" || strings.Contains(req.URL.RawQuery, "token") {
						t.Fatal("token transport")
					}
					if scenario == "create" {
						if req.Method != "POST" || req.URL.Path != "/1/cards" || body["name"] != "New task" || body["desc"] != "Description" || len(body) != 3 {
							t.Fatal(req.Method, body)
						}
					} else if req.Method != "PUT" || req.URL.Path != "/1/cards/"+testCard || len(body) != 1 {
						t.Fatal(req.Method, body)
					}
					if body["idList"] != testTesting {
						t.Fatal(body)
					}
					if scenario == "lost-response" {
						return nil, io.ErrUnexpectedEOF
					}
					return response(200, fmt.Sprintf(`{"id":%q,"name":"New task","idBoard":%q,"idList":%q}`, testCard, testBoard, testTesting)), nil
				}
				if strings.Contains(req.URL.Path, "/lists") {
					return response(200, fmt.Sprintf(`[{"id":%q,"name":"Testing","idBoard":%q}]`, testTesting, testBoard)), nil
				}
				board, list, closed := testBoard, testDoing, false
				if scenario == "wrong-board" {
					board = testTesting
				}
				if scenario == "changed" {
					list = testBoard
				}
				if scenario == "already" {
					list = testTesting
				}
				if scenario == "archived" {
					closed = true
				}
				return response(200, fmt.Sprintf(`{"id":%q,"idBoard":%q,"idList":%q,"closed":%t}`, testCard, board, list, closed)), nil
			})
			dest := testTesting
			if scenario == "wrong-list" {
				dest = testCard
			}
			var got MutationResult
			var err error
			if scenario == "create" {
				got, err = c.Create(context.Background(), "token", Resource{ID: testBoard}, CreateInput{Title: "New task", Description: "Description", Destination: dest})
			} else {
				got, err = c.Move(context.Background(), "token", Resource{ID: testBoard}, MoveInput{Item: testCard, Destination: dest, ExpectedStatus: testDoing})
			}
			want := map[string]error{"wrong-list": ErrArguments, "wrong-board": ErrDenied, "changed": ErrChanged, "archived": ErrRejected, "lost-response": ErrOutcomeUnknown}[scenario]
			if !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
			wantWrites := 0
			if scenario == "create" || scenario == "move" || scenario == "lost-response" {
				wantWrites = 1
			}
			if writes != wantWrites {
				t.Fatal("unsafe write count", writes)
			}
			if err == nil && (got.Item.StatusID != testTesting || got.Item.ID != testCard) {
				t.Fatal(got)
			}
		})
	}
}

func TestJiraCreateMetadataAndADF(t *testing.T) {
	for _, scenario := range []string{"ok", "required", "override", "unknown-type", "subtask"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			c := testClient("jira", func(req *http.Request) (*http.Response, error) {
				if req.Method == "POST" {
					writes++
					if req.URL.Path != "/ex/jira/cloud/rest/api/3/issue" {
						t.Fatal(req.URL)
					}
					var body struct{ Fields map[string]any }
					if json.NewDecoder(req.Body).Decode(&body) != nil {
						t.Fatal("json")
					}
					f := body.Fields
					if f["project"].(map[string]any)["id"] != "10" || f["issuetype"].(map[string]any)["id"] != "1" || f["summary"] != "Task" {
						t.Fatal(f)
					}
					desc := f["description"].(map[string]any)
					if desc["type"] != "doc" || len(desc["content"].([]any)) != 2 {
						t.Fatal(desc)
					}
					return response(201, `{"id":"100","key":"ONE-1"}`), nil
				}
				if strings.HasSuffix(req.URL.Path, "/issuetypes") {
					if req.URL.Query().Get("startAt") == "0" {
						return response(200, `{"issueTypes":[{"id":"1","name":"Task"}],"total":2}`), nil
					}
					return response(200, `{"issueTypes":[{"id":"2","name":"Subtask","subtask":true}],"total":2}`), nil
				}
				if req.URL.Query().Get("startAt") == "0" {
					return response(200, `{"fields":[{"fieldId":"project","required":true,"operations":["set"]},{"fieldId":"issuetype","required":true,"operations":["set"]},{"fieldId":"summary","required":true,"operations":["set"]},{"fieldId":"description","operations":["set"]}],"total":5}`), nil
				}
				return response(200, `{"fields":[{"fieldId":"customfield_100","name":"Category","required":true,"operations":["set"]}],"total":5}`), nil
			})
			input := CreateInput{Title: "Task", Description: "Line one\nLine two", Destination: "1", Fields: map[string]any{"customfield_100": "Requested value"}}
			if scenario == "required" {
				input.Fields = nil
			}
			if scenario == "override" {
				input.Fields["project"] = map[string]string{"id": "20"}
			}
			if scenario == "unknown-type" {
				input.Destination = "9"
			}
			if scenario == "subtask" {
				input.Destination = "2"
			}
			result, err := c.Create(context.Background(), "token", Resource{ID: "cloud:10", SiteID: "cloud", ProjectID: "10", URL: "https://site.atlassian.net/browse/ONE"}, input)
			if scenario == "ok" {
				if err != nil || writes != 1 || result.Item.URL != "https://site.atlassian.net/browse/ONE-1" {
					t.Fatal(result, err, writes)
				}
			} else if err == nil || writes != 0 {
				t.Fatal("invalid creation", err, writes)
			}
		})
	}
}

func TestJiraWorkflowTransition(t *testing.T) {
	for _, scenario := range []string{"ok", "missing-required", "unavailable", "wrong-project", "stale", "server-error", "rejected"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			c := testClient("jira", func(req *http.Request) (*http.Response, error) {
				if req.Method == "POST" {
					writes++
					var body struct {
						Transition struct{ ID string }
						Fields     map[string]any
					}
					if json.NewDecoder(req.Body).Decode(&body) != nil || body.Transition.ID != "31" || body.Fields["resolution"] == nil {
						t.Fatal(body)
					}
					if scenario == "server-error" {
						return response(503, `{"private":"secret"}`), nil
					}
					if scenario == "rejected" {
						return response(400, `{"private":"secret"}`), nil
					}
					return response(204, ""), nil
				}
				if strings.HasSuffix(req.URL.Path, "/transitions") {
					if req.URL.Query().Get("expand") != "transitions.fields" {
						t.Fatal("missing metadata")
					}
					return response(200, fmt.Sprintf(`{"transitions":[{"id":"31","name":"Send to QA","isAvailable":%t,"to":{"id":"3","name":"Testing"},"fields":{"resolution":{"required":true,"operations":["set"]}}}]}`, scenario != "unavailable")), nil
				}
				project, status := "10", "2"
				if scenario == "wrong-project" {
					project = "20"
				}
				if scenario == "stale" {
					status = "4"
				}
				return response(200, fmt.Sprintf(`{"id":"100","key":"ONE-1","fields":{"project":{"id":%q},"status":{"id":%q,"name":"Doing"}}}`, project, status)), nil
			})
			input := MoveInput{Item: "ONE-1", Destination: "31", ExpectedStatus: "2", Fields: map[string]any{"resolution": map[string]string{"id": "1"}}}
			if scenario == "missing-required" {
				input.Fields = nil
			}
			result, err := c.Move(context.Background(), "token", Resource{ID: "cloud:10", SiteID: "cloud", ProjectID: "10"}, input)
			want := map[string]error{"missing-required": ErrRejected, "unavailable": ErrArguments, "wrong-project": ErrDenied, "stale": ErrChanged, "server-error": ErrOutcomeUnknown, "rejected": ErrRejected}[scenario]
			if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			wantWrites := 0
			if scenario == "ok" || scenario == "server-error" || scenario == "rejected" {
				wantWrites = 1
			}
			if writes != wantWrites {
				t.Fatal(writes)
			}
			if scenario == "ok" && (result.Status != "moved" || result.Item.Status != "Testing" || result.Item.StatusID != "3") {
				t.Fatal(result)
			}
		})
	}
}

func TestWriteScopesAndRefreshCompatibility(t *testing.T) {
	for _, kind := range []string{"jira", "trello"} {
		for _, scope := range []string{"", "read:me read:jira-work", "read:member:trello read:board:trello"} {
			if (kind == "jira" && strings.Contains(scope, "trello")) || (kind == "trello" && strings.Contains(scope, "jira")) {
				continue
			}
			c := testClient(kind, func(req *http.Request) (*http.Response, error) {
				return response(200, fmt.Sprintf(`{"access_token":"a","refresh_token":"r","expires_in":3600,"scope":%q}`, scope)), nil
			})
			fresh, err := c.Exchange(context.Background(), "code", "verifier")
			if err != nil {
				t.Fatal(err)
			}
			if canWrite(kind, fresh) != (scope == "") {
				t.Fatal("wrong grant", kind, scope)
			}
			refreshed, err := c.Refresh(context.Background(), "refresh")
			if err != nil || canWrite(kind, refreshed) {
				t.Fatal("refresh upgraded read-only grant", err)
			}
		}
	}
}
