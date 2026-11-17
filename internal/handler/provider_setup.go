package handler

// No credential values are ever read into the setup UI.
func providerSetup(kind string) string {
	switch kind {
	case "github":
		return "NUDGY_CODING_ENABLED=true\nNUDGY_GITHUB_APP_ID=<GitHub App ID>\nNUDGY_GITHUB_APP_SLUG=<uygulama adı>\nNUDGY_GITHUB_CLIENT_ID=<Client ID>\nNUDGY_GITHUB_CLIENT_SECRET=<Client secret>\nNUDGY_CODING_ENCRYPTION_KEY=<32-byte base64 anahtar>"
	case "gitlab":
		return "NUDGY_GITLAB_ENABLED=true\nNUDGY_GITLAB_CLIENT_ID=<Application ID>\nNUDGY_GITLAB_CLIENT_SECRET=<Secret>\nNUDGY_CODING_ENCRYPTION_KEY=<32-byte base64 anahtar>"
	case "jira":
		return "NUDGY_JIRA_ENABLED=true\nNUDGY_JIRA_CLIENT_ID=<Client ID>\nNUDGY_JIRA_CLIENT_SECRET=<Secret>\nNUDGY_TRACKER_ENCRYPTION_KEY=<32-byte base64 anahtar>"
	case "trello":
		return "NUDGY_TRELLO_ENABLED=true\nNUDGY_TRELLO_CLIENT_ID=<API key>\nNUDGY_TRELLO_CLIENT_SECRET=<Secret>\nNUDGY_TRACKER_ENCRYPTION_KEY=<32-byte base64 anahtar>"
	case "raindrop":
		return "RAINDROP_CLIENT_ID=<Client ID>\nRAINDROP_CLIENT_SECRET=<Client secret>\nRAINDROP_REDIRECT_URL=<yukarıdaki geri dönüş adresi>"
	}
	return ""
}
