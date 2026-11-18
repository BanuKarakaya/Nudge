package repository

import (
	"context"
	"errors"

	"nudge/internal/domain"
)

var ErrWorkspaceNotAllowed = errors.New("this self-hosted instance does not accept this Slack workspace")

// WorkspaceRestriction is set by the server operator, never by an OAuth caller.
// An empty configured workspace denies all access until setup is complete.
type WorkspaceRestriction interface {
	AllowedWorkspace() (string, bool)
}

func WorkspaceAllowed(r InstallationRepository, team string) bool {
	if policy, ok := r.(WorkspaceRestriction); ok {
		allowed, restricted := policy.AllowedWorkspace()
		return !restricted || (allowed != "" && allowed == team)
	}
	return true
}

type InstallationRepository interface {
	Save(ctx context.Context, installation domain.SlackInstallation) (int64, error)
	GetByUserID(ctx context.Context, userID int64) (domain.SlackInstallation, error)
	UpsertUser(ctx context.Context, teamID, slackUserID string) (int64, error)
	ListUsersByTeam(ctx context.Context, teamID string) ([]domain.WorkspaceUser, error)
	GetUserIDBySlackUserID(ctx context.Context, teamID, slackUserID string) (int64, error)
}
