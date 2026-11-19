export interface IntegrationBinding {
  destination_id: string;
  pixel_id: string;
  additional: boolean;
  retired: boolean;
}

export interface ConversionIntegration {
  id: string;
  source: string;
  account_key: string;
  enabled: boolean;
  contract_version: string;
  allowed_apps: string[];
  allowed_events: string[];
  bindings: IntegrationBinding[];
}

export interface ConversionDestination {
  id: string;
  pixel_id: string;
  name: string;
  enabled: boolean;
  retired_at?: string;
  credential_configured: boolean;
  promotion_dependencies: number;
  created_at: string;
}

export interface DeliveryLastAttempt {
  number?: number;
  outcome?: string;
  reason?: string;
  http_status?: number;
  graph_code?: number;
  graph_subcode?: number;
  events_received?: number;
  fbtrace_id?: string;
  recorded_at?: string;
}

export interface ConversionDelivery {
  id: string;
  pixel_id: string;
  role: 'original' | 'additional';
  state: string;
  reason?: string;
  meta_event_id: string;
  attempts: number;
  created_at?: string;
  last_attempt_at?: string;
  next_attempt_at?: string;
  deadline_at?: string;
  finalized_at?: string;
  confirmed_at?: string;
  last_attempt?: DeliveryLastAttempt;
}

export interface ConversionDeliveryAttempt {
  number: number;
  started_at: string;
  sending_at?: string;
  responded_at?: string;
  recorded_at?: string;
  outcome?: string;
  reason?: string;
  http_status?: number;
  graph_code?: number;
  graph_subcode?: number;
  events_received?: number;
  fbtrace_id?: string;
}

export interface PartnerReportSummary {
  id: string;
  state: string;
  status: number;
  reason?: string;
  next_attempt_at?: string;
}

export interface PartnerReportAttempt {
  number: number;
  outcome?: string;
  reason?: string;
  http_status?: number;
  business_code?: string;
  started_at: string;
  sending_at?: string;
  responded_at?: string;
  recorded_at?: string;
  retry_at?: string;
}

export interface PartnerReportDetail extends PartnerReportSummary {
  attempt_count: number;
  attempts?: PartnerReportAttempt[];
}

export interface ConversionEvent {
  id: string;
  integration_id: string;
  source: string;
  source_event_id: string;
  source_event_type: string;
  event_name: string;
  order_id?: string;
  promotion_id?: string;
  attribution_state: string;
  reason?: string;
  amount?: string;
  currency?: string;
  occurred_at?: string;
  created_at: string;
  enqueued_at?: string;
  deliveries: ConversionDelivery[];
  deliveries_truncated: boolean;
  partner_report?: PartnerReportSummary;
}

export interface ConversionEventDetail extends ConversionEvent {
  partner_reports?: PartnerReportDetail[];
  attempts?: ConversionDeliveryAttempt[];
}

export interface ConversionEventPage {
  events: ConversionEvent[];
  next_cursor: string | null;
}

export interface AllowedDestinationTarget {
  id: string;
  pixel_id: string;
  display_name: string;
}

export interface OrderEligibility {
  state: string;
  reason?: string;
  pixel_id?: string;
  occurred_at?: string;
  time_source?: string;
  fulfilled_at?: string;
}

export interface OrderEvent {
  id?: string;
  business_key: string;
  source_event_type?: string;
  event_name: string;
  expected?: boolean;
  state: string;
  attribution_state?: string;
  reason?: string;
  occurred_at?: string;
  enqueued_at?: string;
  deliveries: ConversionDelivery[];
  deliveries_truncated: boolean;
  legacy?: {
    state: string;
    resolution?: string;
    confirmed_at?: string;
    environment_verified?: boolean;
  };
  partner_report?: PartnerReportSummary;
}

export interface OrderInboxItem {
  id: string;
  source_event_id?: string;
  source_event_type: string;
  state: string;
  reason?: string;
  received_at: string;
}

export interface OrderReconciliationReport {
  order_id: string;
  source: string;
  environment: string;
  integration_id?: string;
  order_type?: string;
  order_status?: string;
  eligibility?: OrderEligibility;
  events: OrderEvent[];
  events_truncated?: boolean;
  inbox_truncated?: boolean;
  inbox?: OrderInboxItem[];
}

export interface SummaryPopulationRow {
  source: string;
  integration_id: string | null;
  event_name: string;
  eligibility: string;
  events: number;
  confirmed: number;
  missing_outbox: number;
  missing_target: number;
  missing_time: number;
}

export interface SummaryTargetRow {
  source: string;
  integration_id: string;
  event_name: string;
  destination_id: string;
  pixel_id: string;
  role: 'original' | 'additional' | string;
  state: string;
  reason?: string;
  deliveries: number;
  oldest_ready_at?: string;
  oldest_ready_queued_at?: string;
  queue_wait_seconds?: number;
  business_confirmation_seconds?: number;
  queue_confirmation_seconds?: number;
}

export interface SummaryReportRow {
  source?: string;
  integration_id: string;
  destination_id: string;
  event_name: string;
  state: string;
  reason?: string;
  reports: number;
  oldest_pending_at?: string;
}

export interface SummaryAlert {
  kind: string;
  subject: string;
  detail: {
    count?: number;
    oldest_at?: string;
    sample_event_id?: string;
    pixel_id?: string;
    heartbeat_at?: string;
    last_progress_at?: string;
  };
  opened_at: string;
}

export interface WorkerHeartbeat {
  worker_type: string;
  instance_id: string;
  heartbeat_at: string;
  last_progress_at?: string;
  paused: boolean;
}

export interface ConversionSummaryData {
  environment?: string;
  from_at?: string;
  to_at?: string;
  population: SummaryPopulationRow[];
  targets: SummaryTargetRow[];
  reports: SummaryReportRow[];
  alerts: SummaryAlert[];
  heartbeats: WorkerHeartbeat[];
}

export interface CAPILog {
  id: string;
  pixel_id: string;
  event_name: string;
  user_id: string | null;
  value: number | null;
  currency: string | null;
  test_event_code?: string | null;
  test: boolean;
  status_code: number;
  payload?: string;
  response?: string;
  created_at: string;
}

export type ConversionTabKey = 'events' | 'orders' | 'summary' | 'legacy';

export type PixelsTabKey = 'pixels' | 'integrations';

export interface ConversionAuditRecord {
  id: string;
  action: string;
  subject_id: string;
  operator_id?: string;
  operator_role?: string;
  reason?: string;
  evidence_reference?: string;
  before_state?: unknown;
  after_state?: unknown;
  created_at: string;
}

export interface ConversionAuditPage {
  records: ConversionAuditRecord[];
  has_more: boolean;
}
