-- Suggested remediation. Suggestion-only in this release: there is no
-- approval or execution table because nothing is ever executed.
CREATE TABLE remediation_proposals (
    id                    bigserial PRIMARY KEY,
    incident_id           bigint      NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    hypothesis_id         bigint      REFERENCES hypotheses (id) ON DELETE CASCADE,
    action_type           text        NOT NULL,
    title                 text        NOT NULL,
    target                text        NOT NULL,
    rationale             text        NOT NULL,
    supporting            jsonb       NOT NULL DEFAULT '[]',
    preconditions         jsonb       NOT NULL DEFAULT '[]',
    expected_outcome      text        NOT NULL,
    risk                  text        NOT NULL CHECK (risk IN ('low','medium','high')),
    required_permissions  jsonb       NOT NULL DEFAULT '[]',
    suggested_command     text        NOT NULL,
    verification_criteria jsonb       NOT NULL DEFAULT '[]',
    rollback              text        NOT NULL,
    mode                  text        NOT NULL CHECK (mode = 'suggestion_only'),
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX remediation_proposals_incident_idx ON remediation_proposals (incident_id);
