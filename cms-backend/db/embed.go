package db

import _ "embed"

// SchemaSQL embeds the unified production schema
//
//go:embed schema.sql
var SchemaSQL string

// SeedSQL embeds initial seed data (configs, domains, templates, novels, chapters)
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

// PaymentPolicySQL adds versioned payment reporting policies and decision storage.
//
//go:embed migrations/2026111910.sql
var PaymentPolicySQL string

// ReaderPaymentPolicySQL initializes first-payment facts and settlement capture.
//
//go:embed migrations/2026111911.sql
var ReaderPaymentPolicySQL string

// SinanPaymentPolicySQL preserves the scoped payment identity and receipt evidence.
//
//go:embed migrations/2026111912.sql
var SinanPaymentPolicySQL string

// ReaderPaymentReviewSQL uses verified subscription and first-payment facts.
//
//go:embed migrations/2026111913.sql
var ReaderPaymentReviewSQL string

// ReaderPaymentRecoverySQL exposes verified subscription facts to conversion decision writers.
//
//go:embed migrations/2026111914.sql
var ReaderPaymentRecoverySQL string

// ReaderSubscriptionEvidenceSQL scopes subscription evidence and recovers eligible pre-send failures.
//
//go:embed migrations/2026111915.sql
var ReaderSubscriptionEvidenceSQL string

// PaymentDomainFoundationSQL introduces channel account and event identities.
//
//go:embed migrations/2026111916.sql
var PaymentDomainFoundationSQL string

// PaymentCheckoutSQL adds persistent checkout requests, attempts and funding.
//
//go:embed migrations/2026111917.sql
var PaymentCheckoutSQL string

// VIPOfferVersionsSQL preserves published VIP terms and account-scoped PayPal plans.
//
//go:embed migrations/2026111918.sql
var VIPOfferVersionsSQL string

// PaymentLedgerSQL adds authoritative balance movements and their evidence.
//
//go:embed migrations/2026111919.sql
var PaymentLedgerSQL string

// PaymentSettlementSQL adds channel settlement imports and bank reconciliation.
//
//go:embed migrations/2026111920.sql
var PaymentSettlementSQL string

// PaymentCheckoutDecisionsSQL records versioned checkout rules and outcomes.
//
//go:embed migrations/2026111921.sql
var PaymentCheckoutDecisionsSQL string

// PaymentAftercareSQL adds scoped channel facts and audited financial work.
//
//go:embed migrations/2026111922.sql
var PaymentAftercareSQL string

// StripeOneTimeCheckoutSQL adds an approved catalog and immutable checkout snapshots.
//
//go:embed migrations/2026111923.sql
var StripeOneTimeCheckoutSQL string

// StripeVIPCheckoutSQL adds Stripe VIP offers, periods and payment allocations.
//
//go:embed migrations/2026111924.sql
var StripeVIPCheckoutSQL string

// PaymentCompletionSQL adds event recovery, verified offer evidence and finance commands.
//
//go:embed migrations/2026111925.sql
var PaymentCompletionSQL string

//go:embed migrations/2026111926.sql
var PaymentRecoverySQL string

//go:embed migrations/2026111927.sql
var PaymentReviewFixesSQL string

// PaymentCatalogAndChannelSQL adds CFG-04 encrypted channel lifecycle and CAT-01..04 unified product catalog.
//
//go:embed migrations/2026111928.sql
var PaymentCatalogAndChannelSQL string

// PaymentVIPSwitchSQL persists a unified VIP channel switch until it is verified.
//
//go:embed migrations/2026111929.sql
var PaymentVIPSwitchSQL string

// PaymentTemplateCatalogFixesSQL adds Stripe catalog revision tracking, template environment scoping, and restricted promotion template FK.
//
//go:embed migrations/2026111930.sql
var PaymentTemplateCatalogFixesSQL string

//go:embed migrations/2026111931.sql
var ConversionCenterReviewSQL string

//go:embed migrations/2026111932.sql
var ConversionBatchSQL string

//go:embed migrations/2026111933.sql
var ConversionThroughputRereviewSQL string

//go:embed migrations/2026111934.sql
var ConversionCredentialHealthSQL string

// RBACPermissionsSQL introduces single-role RBAC tables, built-in roles, and permission lock functions.
//
//go:embed migrations/2026111935.sql
var RBACPermissionsSQL string

// PromotionEnvironmentSQL scopes promotion_links by environment (live/sandbox) and adds env-aware reader promotion functions.
//
//go:embed migrations/2026111936.sql
var PromotionEnvironmentSQL string

// StripeTaxLiabilityRelaxSQL drops fixed tax_liability_account CHECK constraints while preserving historical columns.
//
//go:embed migrations/2026111937.sql
var StripeTaxLiabilityRelaxSQL string

// StripeLocalCurrencyCustomerSQL adds presentment evidence, USD settlement tracking, frozen checkout params, and verified Stripe customer mapping.
//
//go:embed migrations/2026111938.sql
var StripeLocalCurrencyCustomerSQL string

// StripeRefundDisputeMitigationSQL adds account-level auto-mitigation policy toggles,
// dispute original currency/amount columns, and dispute-period entitlement freeze tracking.
//
//go:embed migrations/2026111939.sql
var StripeRefundDisputeMitigationSQL string
