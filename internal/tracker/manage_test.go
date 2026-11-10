package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const memberOne = "111111111111111111111111"
const memberTwo = "222222222222222222222222"
const memberExisting = "333333333333333333333333"
const checklistID = "444444444444444444444444"
const checkitemID = "555555555555555555555555"

func TestTrelloMultipleMemberAssignment(t *testing.T) {
	for _, mode := range []string{"add", "remove", "set", "unknown", "nonmember", "outside-board"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			c := testClient("trello", func(req *http.Request) (*http.Response, error) {
				if req.Method == "PUT" {
					writes++
					var body map[string]any
					json.NewDecoder(req.Body).Decode(&body)
					expected := memberExisting + "," + memberOne + "," + memberTwo
					if mode == "remove" {
						expected = memberExisting
					}
					if mode == "set" {
						expected = ""
					}
					if len(body) != 1 || body["idMembers"] != expected {
						t.Fatal("wrong assignments", body)
					}
					return response(200, fmt.Sprintf(`{"id":%q,"idBoard":%q,"idMembers":[]}`, testCard, testBoard)), nil
				}
				if strings.HasSuffix(req.URL.Path, "/members") {
					return response(200, fmt.Sprintf(`[{"id":%q,"fullName":"Banu"},{"id":%q,"fullName":"Latif"}]`, memberOne, memberTwo)), nil
				}
				board := testBoard
				if mode == "outside-board" {
					board = testTesting
				}
				members := []string{memberExisting}
				if mode == "remove" {
					members = append(members, memberOne, memberTwo)
				}
				raw, _ := json.Marshal(map[string]any{"id": testCard, "idBoard": board, "idMembers": members})
				return response(200, string(raw)), nil
			})
			input := ChangeInput{Operation: "assign", Item: testCard, Mode: mode, IDs: []string{memberOne, memberTwo}}
			if mode == "set" {
				input.IDs = []string{}
			}
			if mode == "nonmember" {
				input.Mode = "add"
				input.IDs = []string{testTesting}
			}
			if mode == "outside-board" {
				input.Mode = "add"
			}
			_, err := c.Change(context.Background(), "token", Resource{ID: testBoard}, input)
			good := mode == "add" || mode == "remove" || mode == "set"
			if good && (err != nil || writes != 1) {
				t.Fatal(err, writes)
			}
			if !good && (err == nil || writes != 0) {
				t.Fatal("unsafe assignment", err, writes)
			}
		})
	}
}
func TestTrelloTaskPatchAndArchive(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		valid  bool
	}{
		{"content", map[string]any{"name": "New title", "desc": "Notes", "due": "2026-12-01T12:00:00Z", "dueComplete": true}, true},
		{"clear", map[string]any{"due": nil, "start": nil, "idLabels": []string{}}, true},
		{"label", map[string]any{"idLabels": []string{testDoing}}, true},
		{"foreign-label", map[string]any{"idLabels": []string{testTesting}}, false},
		{"bad-date", map[string]any{"due": "tomorrow"}, false},
		{"cross-board", map[string]any{"idBoard": testTesting}, false},
		{"membership-bypass", map[string]any{"idMembers": []string{memberOne}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			c := testClient("trello", func(req *http.Request) (*http.Response, error) {
				if req.Method == "PUT" {
					writes++
					var body map[string]any
					json.NewDecoder(req.Body).Decode(&body)
					if _, ok := body["idBoard"]; ok {
						t.Fatal(body)
					}
				}
				if strings.HasSuffix(req.URL.Path, "/labels") {
					return response(200, fmt.Sprintf(`[{"id":%q,"name":"Bug"}]`, testDoing)), nil
				}
				return response(200, fmt.Sprintf(`{"id":%q,"idBoard":%q}`, testCard, testBoard)), nil
			})
			_, err := c.Change(context.Background(), "token", Resource{ID: testBoard}, ChangeInput{Operation: "update", Item: testCard, Fields: tc.fields})
			if tc.valid && (err != nil || writes != 1) {
				t.Fatal(err, writes)
			}
			if !tc.valid && (err == nil || writes != 0) {
				t.Fatal(err, writes)
			}
		})
	}
	for _, op := range []string{"archive", "restore"} {
		c := testClient("trello", func(req *http.Request) (*http.Response, error) {
			if req.Method == "PUT" {
				var body map[string]any
				json.NewDecoder(req.Body).Decode(&body)
				if body["closed"] != (op == "archive") || len(body) != 1 {
					t.Fatal(body)
				}
			}
			return response(200, fmt.Sprintf(`{"id":%q,"idBoard":%q}`, testCard, testBoard)), nil
		})
		if _, err := c.Change(context.Background(), "t", Resource{ID: testBoard}, ChangeInput{Operation: op, Item: testCard}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTrelloChecklistBoundariesAndComments(t *testing.T) {
	for _, op := range []string{"checklist_create", "checklist_rename", "checkitem_add", "checkitem_update", "comment_add", "comment_update"} {
		for _, foreign := range []bool{false, true} {
			t.Run(fmt.Sprint(op, foreign), func(t *testing.T) {
				writes := 0
				c := testClient("trello", func(req *http.Request) (*http.Response, error) {
					if req.Method != "GET" {
						writes++
						if strings.Contains(req.URL.Path, "checkItem/") && req.Method != "PUT" {
							t.Fatal(req.Method)
						}
						return response(200, fmt.Sprintf(`{"id":%q}`, checkitemID)), nil
					}
					if strings.HasSuffix(req.URL.Path, "/checklists") {
						card := testCard
						if foreign {
							card = testTesting
						}
						return response(200, fmt.Sprintf(`[{"id":%q,"idBoard":%q,"idCard":%q,"checkItems":[{"id":%q,"name":"Test","state":"incomplete"}]}]`, checklistID, testBoard, card, checkitemID)), nil
					}
					if strings.Contains(req.URL.Path, "/actions/") {
						card := testCard
						if foreign {
							card = testTesting
						}
						return response(200, fmt.Sprintf(`{"id":%q,"type":"commentCard","data":{"card":{"id":%q}}}`, checkitemID, card)), nil
					}
					return response(200, fmt.Sprintf(`{"id":%q,"idBoard":%q}`, testCard, testBoard)), nil
				})
				in := ChangeInput{Operation: op, Item: testCard, Target: checklistID, Text: "Test"}
				if op == "checkitem_update" {
					in.Target = checkitemID
					in.Fields = map[string]any{"state": "complete"}
				}
				if op == "comment_update" {
					in.Target = checkitemID
				}
				_, err := c.Change(context.Background(), "t", Resource{ID: testBoard}, in)
				rejected := foreign && op != "checklist_create" && op != "comment_add"
				if rejected && (err == nil || writes != 0) {
					t.Fatal("foreign entity changed", err, writes)
				}
				if !rejected && (err != nil || writes != 1) {
					t.Fatal(err, writes)
				}
			})
		}
	}
}
func TestJiraAssignAndClear(t *testing.T) {
	for _, scenario := range []string{"assign", "clear", "multiple", "inactive", "not-assignable", "other-project"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			c := testClient("jira", func(req *http.Request) (*http.Response, error) {
				if req.Method == "PUT" {
					writes++
					if !strings.HasSuffix(req.URL.Path, "/assignee") {
						t.Fatal(req.URL)
					}
					var body map[string]any
					json.NewDecoder(req.Body).Decode(&body)
					if scenario == "clear" {
						if body["accountId"] != nil {
							t.Fatal(body)
						}
					} else if body["accountId"] != "account:1" {
						t.Fatal(body)
					}
					return response(204, ""), nil
				}
				if strings.HasSuffix(req.URL.Path, "/user/assignable/search") {
					if req.URL.Query().Get("accountId") != "account:1" || req.URL.Query().Get("issueKey") != "ONE-1" {
						t.Fatal(req.URL)
					}
					if scenario == "not-assignable" {
						return response(200, `[]`), nil
					}
					return response(200, fmt.Sprintf(`[{"accountId":"account:1","displayName":"User","active":%t}]`, scenario != "inactive")), nil
				}
				project := "10"
				if scenario == "other-project" {
					project = "20"
				}
				return response(200, fmt.Sprintf(`{"id":"100","key":"ONE-1","fields":{"project":{"id":%q}}}`, project)), nil
			})
			in := ChangeInput{Operation: "assign", Item: "ONE-1", Mode: "set", IDs: []string{"account:1"}}
			if scenario == "clear" {
				in.IDs = nil
			}
			if scenario == "multiple" {
				in.IDs = append(in.IDs, "account:2")
			}
			_, err := c.Change(context.Background(), "t", Resource{ID: "cloud:10", SiteID: "cloud", ProjectID: "10"}, in)
			good := scenario == "assign" || scenario == "clear"
			if good && (err != nil || writes != 1) {
				t.Fatal(err, writes)
			}
			if !good && (err == nil || writes != 0) {
				t.Fatal(err, writes)
			}
		})
	}
}
func TestJiraPatchUsesEditMetadataAndDoesNotRequireUnchangedFields(t *testing.T) {
	for _, scenario := range []string{"ok", "unknown", "protected", "required-clear"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			c := testClient("jira", func(req *http.Request) (*http.Response, error) {
				if req.Method == "PUT" {
					writes++
					var body struct{ Fields map[string]any }
					json.NewDecoder(req.Body).Decode(&body)
					if len(body.Fields) != 3 || body.Fields["description"].(map[string]any)["type"] != "doc" || body.Fields["duedate"] != nil {
						t.Fatal(body)
					}
					return response(204, ""), nil
				}
				if strings.HasSuffix(req.URL.Path, "/editmeta") {
					return response(200, `{"fields":{"summary":{"required":true,"operations":["set"]},"description":{"operations":["set"]},"duedate":{"operations":["set"]},"labels":{"operations":["set"]}}}`), nil
				}
				return response(200, `{"id":"100","key":"ONE-1","fields":{"project":{"id":"10"}}}`), nil
			})
			fields := map[string]any{"description": "Notes\nNext line", "duedate": nil, "labels": []string{"bug"}}
			if scenario == "unknown" {
				fields["customfield_1"] = "value"
			}
			if scenario == "protected" {
				fields["project"] = map[string]string{"id": "20"}
			}
			if scenario == "required-clear" {
				fields["summary"] = nil
			}
			_, err := c.Change(context.Background(), "t", Resource{ID: "cloud:10", SiteID: "cloud", ProjectID: "10"}, ChangeInput{Operation: "update", Item: "ONE-1", Fields: fields})
			if scenario == "ok" && (err != nil || writes != 1) {
				t.Fatal(err, writes)
			}
			if scenario != "ok" && (err == nil || writes != 0) {
				t.Fatal(err, writes)
			}
		})
	}
}
func TestJiraArchiveAndCommentResponses(t *testing.T) {
	for _, op := range []string{"archive", "restore", "comment_add", "comment_update"} {
		c := testClient("jira", func(req *http.Request) (*http.Response, error) {
			if req.Method != "GET" {
				if strings.Contains(op, "comment") {
					var body map[string]any
					json.NewDecoder(req.Body).Decode(&body)
					if body["body"].(map[string]any)["type"] != "doc" {
						t.Fatal(body)
					}
					return response(201, `{"id":"99"}`), nil
				}
				var body struct{ IssueIDsOrKeys []string }
				json.NewDecoder(req.Body).Decode(&body)
				if len(body.IssueIDsOrKeys) != 1 || body.IssueIDsOrKeys[0] != "ONE-1" {
					t.Fatal(body)
				}
				return response(200, `{"numberOfIssuesUpdated":1,"errors":{}}`), nil
			}
			if strings.Contains(req.URL.Path, "/comment/") {
				return response(200, `{"id":"99"}`), nil
			}
			return response(200, `{"id":"100","key":"ONE-1","fields":{"project":{"id":"10"}}}`), nil
		})
		if _, err := c.Change(context.Background(), "t", Resource{ID: "cloud:10", SiteID: "cloud", ProjectID: "10"}, ChangeInput{Operation: op, Item: "ONE-1", Target: "99", Text: "Comment"}); err != nil {
			t.Fatal(op, err)
		}
	}
}
func TestManagementRejectsDeleteAndRedactsUncertainWrites(t *testing.T) {
	for _, op := range []string{"delete", "update"} {
		writes := 0
		c := testClient("trello", func(req *http.Request) (*http.Response, error) {
			if req.Method != "GET" {
				writes++
				return response(500, `{"secret":"private-token"}`), nil
			}
			return response(200, fmt.Sprintf(`{"id":%q,"idBoard":%q}`, testCard, testBoard)), nil
		})
		_, err := c.Change(context.Background(), "t", Resource{ID: testBoard}, ChangeInput{Operation: op, Item: testCard, Fields: map[string]any{"name": "New"}})
		if op == "delete" && (!errors.Is(err, ErrArguments) || writes != 0) {
			t.Fatal(err, writes)
		}
		if op == "update" && (!errors.Is(err, ErrOutcomeUnknown) || writes != 1 || strings.Contains(err.Error(), "private-token")) {
			t.Fatal(err, writes)
		}
	}
}

func TestJiraSubtaskCreationChecksParentProject(t *testing.T) {
	for _, parent := range []string{"ONE-1", "100", "TWO-1"} {
		t.Run(parent, func(t *testing.T) {
			writes := 0
			c := testClient("jira", func(req *http.Request) (*http.Response, error) {
				if req.Method == "POST" {
					writes++
					var body struct{ Fields map[string]any }
					json.NewDecoder(req.Body).Decode(&body)
					key := "key"
					if parent == "100" {
						key = "id"
					}
					if body.Fields["parent"].(map[string]any)[key] != parent {
						t.Fatal(body)
					}
					return response(201, `{"id":"101","key":"ONE-2"}`), nil
				}
				if strings.HasSuffix(req.URL.Path, "/issuetypes") {
					return response(200, `{"issueTypes":[{"id":"2","name":"Subtask","subtask":true}],"total":1}`), nil
				}
				if strings.Contains(req.URL.Path, "/createmeta/") {
					return response(200, `{"fields":[{"fieldId":"project","operations":["set"]},{"fieldId":"issuetype","operations":["set"]},{"fieldId":"summary","operations":["set"]},{"fieldId":"parent","required":true,"operations":["set"]}],"total":4}`), nil
				}
				project, key := "10", "ONE-1"
				if parent == "TWO-1" {
					project = "20"
					key = parent
				}
				return response(200, fmt.Sprintf(`{"id":"100","key":%q,"fields":{"project":{"id":%q}}}`, key, project)), nil
			})
			_, err := c.Create(context.Background(), "t", Resource{ID: "cloud:10", SiteID: "cloud", ProjectID: "10"}, CreateInput{Title: "Subtask", Destination: "2", Parent: parent})
			if parent == "TWO-1" {
				if !errors.Is(err, ErrDenied) || writes != 0 {
					t.Fatal(err, writes)
				}
			} else if err != nil || writes != 1 {
				t.Fatal(err, writes)
			}
		})
	}
}
func TestJiraMemberPaginationDoesNotStopAtFilteredEmptyPage(t *testing.T) {
	c := testClient("jira", func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("project") != "10" {
			t.Fatal("unscoped directory", req.URL)
		}
		return response(200, `[]`), nil
	})
	result, err := c.Lookup(context.Background(), "t", Resource{ID: "cloud:10", SiteID: "cloud", ProjectID: "10"}, LookupInput{Kind: "members"})
	if err != nil || result.(MemberPage).NextCursor != "100" {
		t.Fatal(result, err)
	}
}
func (p *writeFake) Lookup(context.Context, string, Resource, LookupInput) (any, error) {
	return []Member{{ID: "user"}}, nil
}
func (p *writeFake) Change(context.Context, string, Resource, ChangeInput) (MutationResult, error) {
	p.moves.Add(1)
	return MutationResult{Status: "updated"}, p.failure
}
func TestManagementServiceScopeAndReplay(t *testing.T) {
	s, store, p := writeFixture(t)
	in := ChangeInput{Operation: "assign", Item: "item", Mode: "add", IDs: []string{"member"}}
	for range 2 {
		if _, err := s.Change(context.Background(), Identity{1, "T"}, "event", "selected", in); err != nil {
			t.Fatal(err)
		}
	}
	if p.moves.Load() != 1 {
		t.Fatal("duplicate mutation")
	}
	if _, err := s.Change(context.Background(), Identity{1, "T"}, "other", "other", in); !errors.Is(err, ErrDenied) {
		t.Fatal("unselected resource", err)
	}
	if _, err := s.Change(context.Background(), Identity{1, "other"}, "other", "selected", in); !errors.Is(err, ErrDenied) {
		t.Fatal("wrong workspace", err)
	}
	c := store.connections["trello"]
	c.Credentials.Scopes = "read:board:trello"
	store.connections["trello"] = c
	if _, err := s.Change(context.Background(), Identity{1, "T"}, "other", "selected", in); !errors.Is(err, ErrWriteScope) {
		t.Fatal("read-only write", err)
	}
	jira, _, _ := connectFixture(t, "jira")
	if _, err := jira.Lookup(context.Background(), Identity{1, "T"}, "selected", LookupInput{Kind: "members"}); !errors.Is(err, ErrUserScope) {
		t.Fatal("missing Jira directory grant", err)
	}
}
