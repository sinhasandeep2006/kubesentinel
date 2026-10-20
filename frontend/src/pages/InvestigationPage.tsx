import { Eye, History, Lightbulb, ShieldCheck, Wrench } from 'lucide-react'
import { Link, useParams } from 'react-router'
import { useEvidence, useHypotheses, useIncident, useRemediations, useTimeline, useVerification } from '../api/client'
import type { Incident } from '../api/types'
import { Panel, QueryState, SeverityBadge, StatusBadge, Time } from '../components/ui'
import { CapabilitiesStrip } from '../features/investigation/CapabilitiesStrip'
import { EvidenceList } from '../features/investigation/EvidenceList'
import { HypothesisList } from '../features/investigation/HypothesisList'
import { Timeline } from '../features/investigation/Timeline'
import { VerificationList } from '../features/investigation/VerificationList'
import { RemediationList } from '../features/remediation/RemediationList'

const isEmpty = (d: unknown[]) => d.length === 0

function Header({ i }: { i: Incident }) {
  const ids: [string, string][] = [
    ['namespace', i.namespace],
    ['workload', `${i.workload_kind}/${i.workload_name}`],
    ['pod', i.pod],
    ['container', i.container],
    ['pod uid', i.pod_uid],
    ['detector', i.detector],
  ]
  return (
    <header className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-sm text-zinc-500">incident #{i.id}</span>
        <SeverityBadge severity={i.severity} />
        <StatusBadge status={i.status} />
      </div>
      <h1 className="text-lg font-semibold text-zinc-100">{i.summary}</h1>
      <dl className="flex flex-wrap gap-x-5 gap-y-1 text-xs">
        {ids.map(([k, v]) => (
          <div key={k} className="flex gap-1.5">
            <dt className="text-zinc-500">{k}</dt>
            <dd className="font-mono break-all text-zinc-200">{v || 'not reported'}</dd>
          </div>
        ))}
      </dl>
      <p className="flex flex-wrap gap-x-5 text-xs text-zinc-500">
        <span>
          detected <Time value={i.detected_at} absolute />
        </span>
        <span>
          updated <Time value={i.updated_at} absolute />
        </span>
        {i.resolved_at && (
          <span>
            resolved <Time value={i.resolved_at} absolute />
          </span>
        )}
      </p>
    </header>
  )
}

export function InvestigationPage() {
  const { id = '' } = useParams()
  const incident = useIncident(id)
  const timeline = useTimeline(id)
  const evidence = useEvidence(id)
  const hypotheses = useHypotheses(id)
  const verification = useVerification(id)
  const remediations = useRemediations(id)

  return (
    <div className="space-y-5">
      <Link to="/" className="rounded text-sm text-zinc-400 hover:text-zinc-100">
        ← Incident queue
      </Link>
      <QueryState query={incident} label={`incident #${id}`}>
        {(i) => <Header i={i} />}
      </QueryState>
      <CapabilitiesStrip />

      <div className="grid gap-6 lg:grid-cols-12">
        <div className="lg:col-span-5">
          <Panel title="Timeline" icon={<History aria-hidden className="size-4" />} note="State changes, observations and verification checks, oldest first.">
            <QueryState query={timeline} label="timeline" isEmpty={isEmpty}>
              {(entries) => <Timeline entries={entries} />}
            </QueryState>
          </Panel>
        </div>

        <div className="space-y-6 lg:col-span-7">
          <Panel
            title="Hypotheses"
            icon={<Lightbulb aria-hidden className="size-4 text-violet-300" />}
            note="Possible explanations ranked by rules. Not proven root causes."
          >
            <QueryState query={hypotheses} label="hypotheses" isEmpty={isEmpty} empty="No hypotheses yet: the diagnosis is pending or inconclusive.">
              {(items) => <HypothesisList items={items} />}
            </QueryState>
          </Panel>
          <Panel
            title="Suggested remediation"
            icon={<Wrench aria-hidden className="size-4" />}
            note="Suggestions only. Nothing is applied to the cluster."
          >
            <QueryState
              query={remediations}
              label="remediation suggestions"
              isEmpty={isEmpty}
              empty="No safe remediation to suggest. Use the hypotheses' next steps to investigate further."
            >
              {(items) => <RemediationList items={items} />}
            </QueryState>
          </Panel>
          <Panel
            title="Evidence"
            icon={<Eye aria-hidden className="size-4 text-sky-300" />}
            note="Observed facts with their source, in order of observation."
          >
            <QueryState query={evidence} label="evidence" isEmpty={isEmpty} empty="No evidence has been collected for this incident yet.">
              {(items) => <EvidenceList items={items} />}
            </QueryState>
          </Panel>
          <Panel
            title="Verification"
            icon={<ShieldCheck aria-hidden className="size-4" />}
            note="An incident is resolved only when a verification check passes."
          >
            <QueryState query={verification} label="verification results" isEmpty={isEmpty} empty="No verification checks have run yet.">
              {(checks) => <VerificationList checks={checks} />}
            </QueryState>
          </Panel>
        </div>
      </div>
    </div>
  )
}
