package db

import _ "embed"

//go:embed schema.sql
var SchemaSQL string

//go:embed migrations/2026112801.sql
var InitialDataSQL string
