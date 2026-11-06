package migrations

import _ "embed"

//go:embed 008_slack_jobs.sql
var SlackJobSchema string
