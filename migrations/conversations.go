package migrations

import _ "embed"

//go:embed 004_conversations.sql
var ConversationSchema string
