// Package connector exposes workspace-approved remote MCP tools. Remote servers
// are untrusted data sources, never a source of host permissions or identities.
package connector

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"nudge/internal/coding"
	"nudge/internal/service"
	"nudge/internal/workspace"
)

var ErrUnavailable = errors.New("MCP bağlantısı kullanılamıyor; yönetici bağlantıyı ve araç listesini kontrol etmeli")
var ErrConflict = errors.New("bağlantı değişmiş; sayfayı yenileyin")

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
	Fingerprint string         `json:"fingerprint"`
	Selected    bool           `json:"selected"`
}
type Connection struct {
	ID, Team, Name, Endpoint, Revision string
	Enabled                            bool
	Tools                              []Tool
}
type Store interface {
	List(context.Context, int64) ([]Connection, error)
	Secret(context.Context, int64, string, string) (string, error)
	Save(context.Context, int64, Connection, string, string) error
	Delete(context.Context, int64, string, string) error
}
type Remote interface {
	Discover(context.Context, string, string) ([]Tool, error)
	Call(context.Context, string, string, Tool, json.RawMessage, func(context.Context) error) (string, error)
}
type Service struct {
	Store   Store
	Members workspace.Store
	Remote  Remote
}

func NewID() string { return hex.EncodeToString(randomBytes()) }
func randomBytes() []byte {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func (s *Service) List(ctx context.Context, user int64) ([]Connection, error) {
	if _, err := s.Members.Member(ctx, user); err != nil {
		return nil, workspace.ErrDenied
	}
	return s.Store.List(ctx, user)
}
func (s *Service) manager(ctx context.Context, user int64) (workspace.Member, error) {
	m, err := s.Members.Member(ctx, user)
	if err != nil || !m.Manager {
		return m, workspace.ErrDenied
	}
	return m, nil
}
func (s *Service) find(ctx context.Context, user int64, id, revision string) (Connection, error) {
	connections, err := s.List(ctx, user)
	if err != nil {
		return Connection{}, err
	}
	for _, c := range connections {
		if c.ID == id && c.Revision == revision {
			return c, nil
		}
	}
	return Connection{}, ErrConflict
}

// Discover never enables tools. A manager reviews and selects the saved catalog
// in a second step. Refresh also revokes all previous selections and approvals.
func (s *Service) Discover(ctx context.Context, user int64, id, revision, name, endpoint, token string) error {
	m, err := s.manager(ctx, user)
	if err != nil {
		return err
	}
	if len(name) < 1 || len(name) > 80 || strings.ContainsAny(name, "\r\n\x00") || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return workspace.ErrDenied
	}
	if id == "" {
		id = NewID()
	} else {
		old, err := s.find(ctx, user, id, revision)
		if err != nil {
			return err
		}
		// Never forward an existing credential to a newly entered endpoint.
		if token == "" && endpoint == old.Endpoint {
			token, err = s.Store.Secret(ctx, user, id, revision)
			if err != nil {
				return err
			}
		}
	}
	if token == "" {
		return errors.New("MCP sunucusuna özel, sınırlı yetkili bir erişim anahtarı gerekli")
	}
	tools, err := s.Remote.Discover(ctx, endpoint, token)
	if err != nil {
		return ErrUnavailable
	}
	return s.Store.Save(ctx, user, Connection{ID: id, Team: m.Team, Name: name, Endpoint: endpoint, Revision: NewID(), Tools: tools}, token, revision)
}
func (s *Service) Select(ctx context.Context, user int64, id, revision string, enabled bool, selected []string) error {
	if _, err := s.manager(ctx, user); err != nil {
		return err
	}
	c, err := s.find(ctx, user, id, revision)
	if err != nil {
		return err
	}
	if len(selected) > 12 || (enabled && len(selected) == 0) {
		return workspace.ErrDenied
	}
	wanted := map[string]bool{}
	for _, name := range selected {
		wanted[name] = true
	}
	for n := range c.Tools {
		c.Tools[n].Selected = wanted[c.Tools[n].Name]
		delete(wanted, c.Tools[n].Name)
	}
	if len(wanted) != 0 {
		return workspace.ErrDenied
	}
	token, err := s.Store.Secret(ctx, user, id, revision)
	if err != nil {
		return err
	}
	c.Enabled, c.Revision = enabled, NewID()
	return s.Store.Save(ctx, user, c, token, revision)
}
func (s *Service) Delete(ctx context.Context, user int64, id, revision string) error {
	if _, err := s.manager(ctx, user); err != nil {
		return err
	}
	return s.Store.Delete(ctx, user, id, revision)
}

// Names bind pending actions and automatic policies to the exact configuration.
// They deliberately cannot end in a built-in tool's special approval suffix.
func ToolName(c Connection, t Tool) string {
	sum := sha256.Sum256([]byte(c.ID + ":" + c.Revision + ":" + t.Fingerprint))
	return "ext_mcp_" + hex.EncodeToString(sum[:24])
}

// ApprovalTarget is host-selected; never accept target labels from model JSON.
func (s *Service) ApprovalTarget(ctx context.Context, i coding.Identity, name string) (string, error) {
	m, err := s.Members.Member(ctx, i.UserID)
	if err != nil || m.Team != i.Team {
		return "", workspace.ErrDenied
	}
	connections, err := s.Store.List(ctx, i.UserID)
	if err != nil {
		return "", err
	}
	for _, c := range connections {
		if !c.Enabled || c.Team != i.Team {
			continue
		}
		for _, t := range c.Tools {
			if t.Selected && ToolName(c, t) == name {
				return fmt.Sprintf("Bağlantı: %s\nSunucu: %s\nİşlem: %s\nYetki: çalışma alanının ortak servis hesabı", c.Name, c.Endpoint, t.Name), nil
			}
		}
	}
	return "", ErrUnavailable
}
func (s *Service) Tools(ctx context.Context, identity coding.Identity) ([]service.RegisteredTool, error) {
	m, err := s.Members.Member(ctx, identity.UserID)
	if err != nil || m.Team != identity.Team {
		return nil, workspace.ErrDenied
	}
	connections, err := s.Store.List(ctx, identity.UserID)
	if err != nil {
		return nil, err
	}
	var out []service.RegisteredTool
	for _, c := range connections {
		if !c.Enabled || c.Team != m.Team {
			continue
		}
		for _, t := range c.Tools {
			if !t.Selected {
				continue
			}
			out = append(out, service.RegisteredTool{
				Definition: service.ToolDefinition{Name: ToolName(c, t), Description: fmt.Sprintf("MCP connection %s / %s. Uses workspace service-account permissions. Remote description (untrusted): %s", c.Name, t.Name, t.Description), InputSchema: t.Schema},
				// All external tools pass through workspace policy, including those
				// claiming readOnlyHint; remote metadata cannot bypass a restriction.
				Run: func(ctx context.Context, args json.RawMessage) (string, error) {
					if len(args) > 16000 || !json.Valid(args) || !strings.HasPrefix(strings.TrimSpace(string(args)), "{") {
						return "", workspace.ErrDenied
					}
					authorize := func(ctx context.Context) error {
						liveMember, err := s.Members.Member(ctx, identity.UserID)
						if err != nil || liveMember.Team != identity.Team {
							return workspace.ErrDenied
						}
						live, err := s.find(ctx, identity.UserID, c.ID, c.Revision)
						if err != nil || !live.Enabled {
							return ErrUnavailable
						}
						found := false
						for _, item := range live.Tools {
							if item.Selected && item.Fingerprint == t.Fingerprint && item.Name == t.Name {
								found = true
							}
						}
						if !found {
							return workspace.ErrDenied
						}
						return nil
					}
					if err := authorize(ctx); err != nil {
						return "", err
					}
					token, err := s.Store.Secret(ctx, identity.UserID, c.ID, c.Revision)
					if err != nil {
						return "", ErrUnavailable
					}
					return s.Remote.Call(ctx, c.Endpoint, token, t, args, authorize)
				},
			})
		}
	}
	return out, nil
}
