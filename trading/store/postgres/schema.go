package postgres

import _ "embed"

// Schema is embedded for isolated integration tests. The canonical S78
// integration copies this DDL into the next ordered repository migration.
//
//go:embed schema.sql
var Schema string
