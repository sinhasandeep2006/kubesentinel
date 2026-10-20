import { ArrowRightLeft, Eye, ShieldCheck } from 'lucide-react'
import type { TimelineEntry, TimelineKind } from '../../api/types'
import { Time } from '../../components/ui'

const KIND: Record<TimelineKind, { label: string; Icon: typeof Eye; cls: string }> = {
  transition: { label: 'State change', Icon: ArrowRightLeft, cls: 'border-zinc-600 text-zinc-300' },
  evidence: { label: 'Observed', Icon: Eye, cls: 'border-sky-700 text-sky-300' },
  verification: { label: 'Verification', Icon: ShieldCheck, cls: 'border-emerald-800 text-emerald-300' },
}

export function Timeline({ entries }: { entries: TimelineEntry[] }) {
  return (
    <ol className="relative ml-3 border-l border-zinc-800">
      {entries.map((e, i) => {
        const k = KIND[e.kind] ?? KIND.transition
        return (
          <li key={`${e.at}-${i}`} className="relative mb-4 pl-6">
            <span className={`absolute -left-3 top-0.5 flex size-6 items-center justify-center rounded-full border bg-zinc-950 ${k.cls}`}>
              <k.Icon aria-hidden className="size-3.5" />
            </span>
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
              <Time value={e.at} absolute />
              <span className={`text-xs uppercase ${k.cls.split(' ')[1]}`}>{k.label}</span>
            </div>
            <p className="font-medium text-zinc-100">
              {e.evidence_id != null ? (
                <a href={`#evidence-${e.evidence_id}`} className="rounded underline-offset-2 hover:underline">
                  {e.title}
                </a>
              ) : (
                e.title
              )}
            </p>
            {e.detail && <p className="text-sm text-zinc-400">{e.detail}</p>}
          </li>
        )
      })}
    </ol>
  )
}
