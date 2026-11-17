package db

import _ "embed"

// SchemaSQL embeds the unified production schema
//
//go:embed schema.sql
var SchemaSQL string

// SeedSQL embeds initial seed data (superadmin, configs, domains, templates, novels, chapters)
//
//go:embed seed.sql
var SeedSQL string

// ChapterPublicationSQL upgrades the immutable 2026111701 baseline.
//
//go:embed migrations/2026111702.sql
var ChapterPublicationSQL string
