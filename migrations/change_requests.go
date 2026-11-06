package migrations

import _ "embed"

//go:embed 009_merge_intents.sql
var MergeIntentSchema string
