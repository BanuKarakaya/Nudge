package migrations

import _ "embed"

//go:embed 005_analytics.sql
var AnalyticsSchema string
