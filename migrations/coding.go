package migrations

import _ "embed"

//go:embed 007_coding.sql
var CodingSchema string

//go:embed gitlab.sql
var GitLabSchema string
