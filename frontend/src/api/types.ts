// Mirrors the backend API types. Times are RFC 3339 UTC strings.

export type SourceStatus = 'available' | 'degraded' | 'unavailable'

export interface CapabilitySource {
  name: string
  status: SourceStatus
  detail: string
}

export interface Capabilities {
  sources: CapabilitySource[]
}

export const INCIDENT_STATUSES = [
  'detected',
  'investigating',
  'diagnosed',
  'awaiting_approval',
  'remediating',
  'verifying',
  'resolved',
  'reopened',
  'dismissed',
] as const
export type IncidentStatus = (typeof INCIDENT_STATUSES)[number]

export const SEVERITIES = ['warning', 'critical'] as const
export type Severity = (typeof SEVERITIES)[number]

export interface Incident {
  id: number
  namespace: string
  workload_kind: string
  workload_name: string
  pod: string
  pod_uid: string
  container: string
  detector: string
  status: IncidentStatus
  severity: Severity
  summary: string
  detected_at: string
  updated_at: string
  resolved_at: string | null
}

export type EvidenceType =
  | 'restart_signal'
  | 'detector_signal'
  | 'termination'
  | 'kubernetes_events'
  | 'previous_logs'
  | 'metrics'
  | 'pod_status'

export type Availability = 'available' | 'unavailable' | 'error'

export interface RestartSignalPayload {
  restart_count: number
  restart_delta: number
  window_seconds: number
  waiting_reason: string
}

/** Condition detectors: image pull, scheduling, readiness, OOM. */
export interface DetectorSignalPayload {
  detector: string
  reason: string
  message: string
  waiting_reason: string
  restart_count: number
}

/** Prometheus usage over the window. null means Prometheus had no data, never zero. */
export interface MetricsPayload {
  scope: 'container' | 'pod'
  window_seconds: number
  memory_max_bytes: number | null
  memory_limit_bytes: number | null
  memory_ratio: number | null
  cpu_throttled_ratio: number | null
  cpu_periods: number | null
}

export interface TerminationPayload {
  reason: string
  exit_code: number
  finished_at: string
}

export interface KubeEvent {
  type: string
  reason: string
  message: string
  count: number
  last_seen: string
}

export interface KubernetesEventsPayload {
  events: KubeEvent[]
}

export interface PreviousLogsPayload {
  lines: string[]
  truncated: boolean
}

export interface PodStatusPayload {
  phase: string
  ready: boolean
  running: boolean
  restart_count: number
}

export interface Evidence {
  id: number
  incident_id: number
  // string fallback: unknown types from a newer backend render as raw JSON.
  type: EvidenceType | (string & {})
  source: string
  resource: string
  observed_at: string
  summary: string
  availability: Availability
  detail: string
  // Shape depends on `type`; narrowed by the payload renderers.
  payload: Record<string, unknown>
}

export interface EvidenceRef {
  evidence_id: number | null
  note: string
}

export interface Hypothesis {
  id: number
  rank: number
  category: string
  title: string
  score: number
  rationale: string
  supporting: EvidenceRef[]
  contradicting: EvidenceRef[]
  missing_evidence: string[]
  alternatives: string[]
  next_steps: string[]
  method: string
}

export type TimelineKind = 'transition' | 'evidence' | 'verification'

export interface TimelineEntry {
  at: string
  kind: TimelineKind
  title: string
  detail: string
  evidence_id: number | null
}

export type VerificationOutcome = 'passed' | 'failed' | 'inconclusive'

export interface VerificationCriterion {
  name: string
  passed: boolean
  observed: string
}

export interface Verification {
  id: number
  checked_at: string
  outcome: VerificationOutcome
  summary: string
  criteria: VerificationCriterion[]
}

export interface ListResponse<T> {
  items: T[]
}

export interface IncidentFilters {
  status?: string
  namespace?: string
  severity?: string
}

/** Suggested remediation. Suggestion-only: KubeSentinel never executes these. */
export interface Proposal {
  id: number
  incident_id: number
  hypothesis_id: number | null
  action_type: string
  title: string
  target: string
  rationale: string
  supporting: EvidenceRef[]
  preconditions: string[]
  expected_outcome: string
  risk: 'low' | 'medium' | 'high'
  required_permissions: string[]
  suggested_command: string
  verification_criteria: string[]
  rollback: string
  mode: 'suggestion_only'
}
