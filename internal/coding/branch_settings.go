package coding

import "context"

type BranchRule struct {
	Team     string `json:"team_id"`
	User     string `json:"slack_user_id"`
	Template string `json:"template"`
}
type BranchScope struct {
	Team string `json:"team_id"`
	Name string `json:"team_name"`
	User string `json:"slack_user_id"`
}
type BranchSettings struct {
	Rules  []BranchRule  `json:"rules"`
	Scopes []BranchScope `json:"scopes"`
}

// The current dashboard is operator-only, across all installations.
// A future member UI must authenticate and constrain scope before using this API.
type BranchSettingsStore interface {
	BranchRules
	Settings(context.Context) (BranchSettings, error)
	SaveRule(context.Context, BranchRule) error
}
