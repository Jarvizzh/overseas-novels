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

// ReaderPerformanceSQL separates chapter previews and tracks catalog generations.
//
//go:embed migrations/2026111703.sql
var ReaderPerformanceSQL string

// ReaderQueueAndShelfSQL adds stable scheduling and payment task aggregates.
//
//go:embed migrations/2026111704.sql
var ReaderQueueAndShelfSQL string

// ConversionReportingSQL installs durable conversion queues and Reader snapshots.
//
//go:embed migrations/2026111901.sql
var ConversionReportingSQL string

// ReaderConversionSQL replaces the legacy payment enqueue trigger.
//
//go:embed migrations/2026111902.sql
var ReaderConversionSQL string

// LegacyConversionSQL adds the historical conversion import ledger.
//
//go:embed migrations/2026111903.sql
var LegacyConversionSQL string

// ConversionReconciliationSQL indexes external order lookups.
//
//go:embed migrations/2026111904.sql
var ConversionReconciliationSQL string

// ConversionOperationsSQL adds audited actions, repair evidence and alert state.
//
//go:embed migrations/2026111905.sql
var ConversionOperationsSQL string

// ConversionReviewSQL separates settlement evidence from the queue and isolates row locks.
//
//go:embed migrations/2026111906.sql
var ConversionReviewSQL string

// SinanConversionSQL adds report replay snapshots and live route validation.
//
//go:embed migrations/2026111907.sql
var SinanConversionSQL string

// SinanReviewSQL adds structured partner receipts and account-scoped promotion routing.
//
//go:embed migrations/2026111908.sql
var SinanReviewSQL string

// SinanRecheckSQL versions Inbox digests and preserves partner attempt identity.
//
//go:embed migrations/2026111909.sql
var SinanRecheckSQL string
