import { Check, X } from 'lucide-react'
import type { Verification, VerificationOutcome } from '../../api/types'
import { Time } from '../../components/ui'

const OUTCOME: Record<VerificationOutcome, string> = {
  passed: 'border-emerald-800 text-emerald-300',
  failed: 'border-red-800 text-red-300',
  inconclusive: 'border-amber-800 text-amber-300',
}

export function VerificationList({ checks }: { checks: Verification[] }) {
  return (
    <ul className="space-y-3">
      {checks.map((v) => (
        <li key={v.id} className="rounded border border-zinc-800 bg-zinc-900/40 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className={`rounded border px-1.5 py-0.5 text-xs uppercase ${OUTCOME[v.outcome] ?? 'border-zinc-700'}`}>
              {v.outcome}
            </span>
            <Time value={v.checked_at} absolute />
          </div>
          <p className="mt-1 text-sm text-zinc-300">{v.summary}</p>
          {v.criteria.length > 0 && (
            <ul className="mt-2 space-y-1 text-xs">
              {v.criteria.map((c) => (
                <li key={c.name} className="flex gap-2">
                  {c.passed ? (
                    <Check aria-label="passed" className="size-3.5 shrink-0 text-emerald-400" />
                  ) : (
                    <X aria-label="not passed" className="size-3.5 shrink-0 text-red-400" />
                  )}
                  <span className="text-zinc-300">{c.name}</span>
                  <span className="font-mono text-zinc-500">{c.observed}</span>
                </li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  )
}
