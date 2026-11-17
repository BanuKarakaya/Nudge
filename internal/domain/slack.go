package domain

type SlackInstallation struct {
	TeamID      string
	TeamName    string
	BotToken    string
	SlackUserID string
}

type SlackMember struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
	IsBot   bool   `json:"is_bot"`
}

type WorkspaceUser struct {
	ID          int64
	TeamID      string
	SlackUserID string
}
