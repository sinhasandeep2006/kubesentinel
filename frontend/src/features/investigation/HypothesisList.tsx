import { Check, Copy, Lightbulb } from 'lucide-react'
import { useState } from 'react'
import type { EvidenceRef, Hypothesis } from '../../api/types'

export function Refs({ label, refs, tone }: { label: string; refs: EvidenceRef[]; tone: string }) {
  if (!refs?.length) return null
  return (
    <div>
      <h4 className={`text-xs font-medium uppercase ${tone}`}>{label}</h4>
      <ul className="mt-0.5 space-y-0.5 text-sm text-zinc-300">
        {refs.map((r, i) => (
          <li key={i}>
            {r.evidence_id != null ? (
              <a href={`#evidence-${r.evidence_id}`} className="mr-1.5 rounded font-mono text-xs text-sky-400 hover:underline">
                evidence #{r.evidence_id}
              </a>
            ) : (
              <span className="mr-1.5 font-mono text-xs text-zinc-500">no linked evidence</span>
            )}
            {r.note}
          </li>
        ))}
      </ul>
    </div>
  )
}

export function Strings({ label, items }: { label: string; items: string[] }) {
  if (!items?.length) return null
  return (
    <div>
      <h4 className="text-xs font-medium text-zinc-400 uppercase">{label}</h4>
      <ul className="mt-0.5 list-disc pl-5 text-sm text-zinc-300">
        {items.map((s) => (
          <li key={s}>{s}</li>
        ))}
      </ul>
    </div>
  )
}

export function CommandLine({ cmd }: { cmd: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () =>
    navigator.clipboard?.writeText(cmd).then(
      () => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      },
      () => setCopied(false),
    )
  return (
    <li className="flex items-start gap-2 rounded bg-zinc-950 px-2 py-1.5">
      <code className="flex-1 font-mono text-xs break-all text-zinc-200 select-all">{cmd}</code>
      <button
        type="button"
        aria-label={copied ? 'Copied' : 'Copy command'}
        className="rounded p-0.5 text-zinc-400 hover:text-zinc-100"
        onClick={copy}
      >
        {copied ? <Check aria-hidden className="size-3.5" /> : <Copy aria-hidden className="size-3.5" />}
      </button>
    </li>
  )
}

export function HypothesisList({ items }: { items: Hypothesis[] }) {
  return (
    <ol className="space-y-3">
      {items.map((h) => (
        <li key={h.id} className="rounded border border-dashed border-violet-800/70 bg-violet-950/10 p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="flex items-center gap-1 rounded border border-dashed border-violet-700 px-1.5 py-0.5 font-medium text-violet-300 uppercase">
              <Lightbulb aria-hidden className="size-3" /> Hypothesis
            </span>
            <span className="text-zinc-400">rank {h.rank}</span>
            <span className="font-mono text-zinc-500">{h.category}</span>
            <span className="ml-auto text-zinc-400">
              ranking score <span className="font-mono text-zinc-200">{h.score}</span>{' '}
              <span className="text-zinc-500">(not a probability)</span>
            </span>
          </div>
          <h3 className="mt-1.5 font-medium text-zinc-100">{h.title}</h3>
          <p className="mt-0.5 text-sm text-zinc-400">{h.rationale}</p>
          <div className="mt-3 space-y-2.5">
            <Refs label="Supporting evidence" refs={h.supporting} tone="text-emerald-400" />
            <Refs label="Contradicting evidence" refs={h.contradicting} tone="text-red-400" />
            <Strings label="Missing evidence" items={h.missing_evidence} />
            <Strings label="Alternative explanations" items={h.alternatives} />
            {h.next_steps?.length > 0 && (
              <div>
                <h4 className="text-xs font-medium text-zinc-400 uppercase">Suggested next steps</h4>
                <p className="text-xs text-zinc-500">Read-only suggestions for a human to run. KubeSentinel never executes them.</p>
                <ul className="mt-1 space-y-1">
                  {h.next_steps.map((c) => (
                    <CommandLine key={c} cmd={c} />
                  ))}
                </ul>
              </div>
            )}
          </div>
          <p className="mt-3 border-t border-zinc-800 pt-2 text-xs text-zinc-500">
            Method: <span className="text-zinc-400">{h.method}</span>
          </p>
        </li>
      ))}
    </ol>
  )
}
