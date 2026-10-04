package domain

type SlackInstallation struct {
	TeamID      string
	TeamName    string
	BotToken    string
	SlackUserID string
}

type SlackMember struct {
	ID      string
	Deleted bool
	IsBot   bool
}

type WorkspaceUser struct {
	ID          int64
	TeamID      string
	SlackUserID string
}
