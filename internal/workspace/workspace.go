// Package workspace separates shared integration scope from personal credentials.
package workspace

import (
	"context"
	"errors"
	"time"
)

var ErrDenied = errors.New("workspace access denied")

type Resource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Member struct {
	UserID       int64  `json:"user_id"`
	Team         string `json:"team_id"`
	Name         string `json:"workspace_name"`
	SlackUser    string `json:"slack_user_id"`
	Manager      bool   `json:"manager"`
	OwnerMissing bool   `json:"owner_missing"`
}
type Scope struct {
	Manager   bool       `json:"-"`
	Enabled   bool       `json:"enabled"`
	Resources []Resource `json:"resources"`
}

func (s Scope) Allows(id string) bool {
	if !s.Enabled {
		return false
	}
	for _, r := range s.Resources {
		if r.ID == id {
			return true
		}
	}
	return false
}

type Policy interface {
	Scope(context.Context, int64, string) (Scope, error)
}
type Store interface {
	Policy
	Member(context.Context, int64) (Member, error)
	SaveScope(context.Context, int64, string, Scope) error
	CreateSession(context.Context, int64, string, time.Time, string) error
	SessionUser(context.Context, string) (int64, error)
	DeleteSession(context.Context, string) error
}

func ValidProvider(kind string) bool {
	return kind == "github" || kind == "gitlab" || kind == "jira" || kind == "trello"
}
