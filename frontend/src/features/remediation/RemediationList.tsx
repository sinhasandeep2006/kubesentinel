import { Wrench } from 'lucide-react'
import type { Proposal } from '../../api/types'
import { CommandLine, Refs, Strings } from '../investigation/HypothesisList'

const riskTone: Record<Proposal['risk'], string> = {
  low: 'border-zinc-600 text-zinc-300',
  medium: 'border-amber-700 text-amber-300',
  high: 'border-red-700 text-red-300',
}

export function RemediationList({ items }: { items: Proposal[] }) {
  return (
    <ol className="space-y-3">
      {items.map((p) => (
        <li key={p.id} className="rounded border border-dashed border-zinc-700 bg-zinc-900/40 p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="flex items-center gap-1 rounded border border-dashed border-zinc-600 px-1.5 py-0.5 font-medium text-zinc-300 uppercase">
              <Wrench aria-hidden className="size-3" /> Suggestion
            </span>
            <span className={`rounded border px-1.5 py-0.5 ${riskTone[p.risk]}`}>{p.risk} risk</span>
            <span className="font-mono text-zinc-500">{p.action_type}</span>
            {p.hypothesis_id != null && <span className="ml-auto text-zinc-500">for hypothesis #{p.hypothesis_id}</span>}
          </div>
          <h3 className="mt-1.5 font-medium text-zinc-100">{p.title}</h3>
          <p className="mt-0.5 text-sm text-zinc-400">{p.rationale}</p>
          <p className="mt-1 font-mono text-xs break-all text-zinc-400">target: {p.target}</p>
          <div className="mt-3 space-y-2.5">
            <Strings label="Check before acting" items={p.preconditions} />
            <div>
              <h4 className="text-xs font-medium text-zinc-400 uppercase">Expected outcome</h4>
              <p className="text-sm text-zinc-300">{p.expected_outcome}</p>
            </div>
            <div>
              <h4 className="text-xs font-medium text-zinc-400 uppercase">Suggested command</h4>
              <p className="text-xs text-zinc-500">
                For a human to review and run. KubeSentinel is read-only and never executes it.
              </p>
              <ul className="mt-1">
                <CommandLine cmd={p.suggested_command} />
              </ul>
            </div>
            <Strings label="Permissions needed (yours, not KubeSentinel's)" items={p.required_permissions} />
            <Strings label="How recovery will be verified" items={p.verification_criteria} />
            <div>
              <h4 className="text-xs font-medium text-zinc-400 uppercase">Rollback</h4>
              <code className="font-mono text-xs break-all text-zinc-300">{p.rollback}</code>
            </div>
            <Refs label="Based on evidence" refs={p.supporting} tone="text-emerald-400" />
          </div>
        </li>
      ))}
    </ol>
  )
}
