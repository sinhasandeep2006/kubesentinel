-- Incidents and their evidence, hypotheses, transitions, and verification results.
-- Metric samples are never stored here; only evidence snapshots.

CREATE TABLE incidents (
    id            bigserial PRIMARY KEY,
    namespace     text        NOT NULL,
    workload_kind text        NOT NULL,
    workload_name text        NOT NULL,
    pod           text        NOT NULL,
    pod_uid       text        NOT NULL,
    container     text        NOT NULL,
    detector      text        NOT NULL,
    status        text        NOT NULL CHECK (status IN ('detected','investigating','diagnosed','awaiting_approval',
                                                         'remediating','verifying','resolved','reopened','dismissed')),
    severity      text        NOT NULL CHECK (severity IN ('warning','critical')),
    summary       text        NOT NULL,
    detected_at   timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    resolved_at   timestamptz
);
CREATE INDEX incidents_status_idx ON incidents (status);
CREATE INDEX incidents_namespace_time_idx ON incidents (namespace, detected_at DESC);
CREATE INDEX incidents_time_idx ON incidents (detected_at DESC);
CREATE INDEX incidents_key_idx ON incidents (pod_uid, container, detector);

-- Append-only audit of lifecycle changes.
CREATE TABLE incident_transitions (
    id          bigserial PRIMARY KEY,
    incident_id bigint      NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    from_status text,
    to_status   text        NOT NULL,
    reason      text        NOT NULL,
    at          timestamptz NOT NULL
);
CREATE INDEX incident_transitions_incident_idx ON incident_transitions (incident_id, at);

CREATE TABLE evidence_records (
    id           bigserial PRIMARY KEY,
    incident_id  bigint      NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    type         text        NOT NULL,
    source       text        NOT NULL,
    resource     text        NOT NULL,
    observed_at  timestamptz NOT NULL,
    summary      text        NOT NULL,
    availability text        NOT NULL CHECK (availability IN ('available','unavailable','error')),
    detail       text        NOT NULL DEFAULT '',
    payload      jsonb       NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX evidence_records_incident_idx ON evidence_records (incident_id, observed_at);

CREATE TABLE hypotheses (
    id               bigserial PRIMARY KEY,
    incident_id      bigint      NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    rank             int         NOT NULL,
    category         text        NOT NULL,
    title            text        NOT NULL,
    score            int         NOT NULL,
    rationale        text        NOT NULL,
    missing_evidence jsonb       NOT NULL DEFAULT '[]',
    alternatives     jsonb       NOT NULL DEFAULT '[]',
    next_steps       jsonb       NOT NULL DEFAULT '[]',
    method           text        NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX hypotheses_incident_idx ON hypotheses (incident_id, rank);

-- evidence_id is NULL for notes that point at no stored evidence.
CREATE TABLE hypothesis_evidence (
    id            bigserial PRIMARY KEY,
    hypothesis_id bigint NOT NULL REFERENCES hypotheses (id) ON DELETE CASCADE,
    evidence_id   bigint REFERENCES evidence_records (id) ON DELETE CASCADE,
    relation      text   NOT NULL CHECK (relation IN ('supporting','contradicting')),
    note          text   NOT NULL
);
CREATE INDEX hypothesis_evidence_hypothesis_idx ON hypothesis_evidence (hypothesis_id);

CREATE TABLE verification_results (
    id          bigserial PRIMARY KEY,
    incident_id bigint      NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    checked_at  timestamptz NOT NULL,
    outcome     text        NOT NULL CHECK (outcome IN ('passed','failed','inconclusive')),
    summary     text        NOT NULL,
    criteria    jsonb       NOT NULL DEFAULT '[]'
);
CREATE INDEX verification_results_incident_idx ON verification_results (incident_id, checked_at);
