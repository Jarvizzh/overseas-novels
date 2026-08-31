package db

import _ "embed"

// SchemaSQL embeds the unified production schema
//
//go:embed schema.sql
var SchemaSQL string

// SeedSQL embeds initial seed data
//
//go:embed seed.sql
var SeedSQL string
