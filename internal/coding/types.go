// Package coding contains repository access and task preparation, independent of
// Slack, Codex and the eventual isolated execution backend.
package coding

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

var (
	ErrConnect     = errors.New("GitHub bağlantısı gerekli; github_connect aracını kullan")
	ErrDenied      = errors.New("bu repo için GitHub App, kullanıcı veya çalışma alanı izni yok")
	ErrUnavailable = errors.New("GitHub bağlantısı şu anda doğrulanamadı")
	ErrNotFound    = errors.New("görev bulunamadı")
	ErrArguments   = errors.New("geçersiz repo veya görev bilgisi")
)

type Credentials struct {
	Access         string    `json:"access"`
	Refresh        string    `json:"refresh"`
	Expires        time.Time `json:"expires"`
	RefreshExpires time.Time `json:"refresh_expires"`
}

type Connection struct {
	UserID int64
	// Resolved from the immutable Slack user record, never supplied by the model.
	Team         string
	GitHubUserID int64
	Login        string
	Credentials  Credentials
}

// Account is safe to display. Never return Connection or Credentials to a tool.
type Account struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// ConnectionBinding is admin-only diagnostic metadata; it contains no secrets.
type ConnectionBinding struct {
	UserID      int64   `json:"user_id"`
	Team        string  `json:"team_id"`
	SlackUserID string  `json:"slack_user_id"`
	Account     Account `json:"github_account"`
}

type RepositoryAccess struct {
	Account      Account      `json:"github_account"`
	Repositories []Repository `json:"repositories"`
}

type Repository struct {
	ID             int64  `json:"id"`
	FullName       string `json:"full_name"`
	DefaultBranch  string `json:"default_branch"`
	InstallationID int64  `json:"installation_id"`
}

type Identity struct {
	UserID                                int64
	Team, Channel, Thread, Event, Request string
}

type Task struct {
	ID          string     `json:"id"`
	Identity    Identity   `json:"-"`
	Provider    string     `json:"provider"`
	Repository  Repository `json:"repository"`
	Instruction string     `json:"instruction"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
}

type OAuthState struct {
	UserID         int64
	Verifier       string
	Expires        time.Time
	Phase          string
	InstallationID int64
}

type Store interface {
	ConnectionBindings(context.Context) ([]ConnectionBinding, error)
	SlackTeam(context.Context, int64) (string, error)
	SaveConnection(context.Context, Connection) error
	// WithConnection locks this user's credential across refresh and persistence.
	WithConnection(context.Context, int64, func(*Connection) error) error
	Disconnect(context.Context, int64) error
	PutOAuthState(context.Context, string, OAuthState) error
	ConsumeOAuthState(context.Context, string) (OAuthState, error)
	CreateDraft(context.Context, Task) (Task, error)
	Task(context.Context, Identity, string) (Task, error)
	CancelDraft(context.Context, Identity, string) (Task, error)
}

type Provider interface {
	InstallationURL(state string) string
	VerifyInstallation(context.Context, string, int64, Account) error
	AuthorizeURL(state, verifier string) string
	Exchange(context.Context, string, string) (Credentials, error)
	Refresh(context.Context, string) (Credentials, error)
	User(context.Context, string) (int64, string, error)
	Repositories(context.Context, string) ([]Repository, error)
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
var taskID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidRepository(name string) bool { return repoName.MatchString(name) }

func RandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
