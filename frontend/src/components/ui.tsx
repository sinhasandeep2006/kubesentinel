import type { UseQueryResult } from '@tanstack/react-query'
import { AlertTriangle, Inbox, Loader2 } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import type { IncidentStatus, Severity } from '../api/types'

/** Renders explicit loading / error / empty states, then children with the data. */
export function QueryState<T>({
  query,
  label,
  isEmpty,
  empty,
  children,
}: {
  query: UseQueryResult<T>
  label: string
  isEmpty?: (data: T) => boolean
  empty?: ReactNode
  children: (data: T) => ReactNode
}) {
  if (query.isPending) {
    return (
      <output className="flex items-center gap-2 py-3 text-sm text-zinc-400">
        <Loader2 aria-hidden className="size-4 animate-spin" /> Loading {label}…
      </output>
    )
  }
  if (query.isError) {
    return (
      <p role="alert" className="flex items-start gap-2 rounded border border-red-900/60 bg-red-950/30 px-3 py-2 text-sm text-red-300">
        <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0" />
        <span>
          Could not load {label}: {query.error.message}
        </span>
      </p>
    )
  }
  if (isEmpty?.(query.data)) {
    return (
      <p className="flex items-center gap-2 py-3 text-sm text-zinc-500">
        <Inbox aria-hidden className="size-4" /> {empty ?? `No ${label}.`}
      </p>
    )
  }
  return <>{children(query.data)}</>
}

const SEVERITY_STYLE: Record<Severity, string> = {
  critical: 'border-red-800 bg-red-950/50 text-red-300',
  warning: 'border-amber-800 bg-amber-950/40 text-amber-300',
}

export function SeverityBadge({ severity }: { severity: Severity }) {
  return (
    <span className={`inline-block rounded border px-1.5 py-0.5 text-xs font-medium uppercase tracking-wide ${SEVERITY_STYLE[severity] ?? 'border-zinc-700 text-zinc-300'}`}>
      {severity}
    </span>
  )
}

function statusStyle(status: IncidentStatus) {
  if (status === 'resolved') return 'border-emerald-800 bg-emerald-950/40 text-emerald-300'
  if (status === 'reopened') return 'border-amber-800 bg-amber-950/40 text-amber-300'
  if (status === 'dismissed') return 'border-zinc-800 text-zinc-500'
  return 'border-zinc-700 bg-zinc-800/60 text-zinc-300'
}

export function StatusBadge({ status }: { status: IncidentStatus }) {
  return (
    <span className={`inline-block rounded border px-1.5 py-0.5 font-mono text-xs ${statusStyle(status)}`}>
      {status.replace('_', ' ')}
    </span>
  )
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
  ['second', 1],
]

/** Current time, refreshed every 30s so relative times stay honest without a refetch. */
function useNow() {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(t)
  }, [])
  return now
}

/** Relative time with the absolute UTC timestamp on hover. */
export function Time({ value, absolute = false }: { value: string; absolute?: boolean }) {
  const now = useNow()
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return <span className="text-zinc-500">{value || 'unknown time'}</span>
  const iso = d.toISOString()
  if (absolute) {
    return (
      <time dateTime={iso} className="font-mono text-xs text-zinc-400">
        {iso.replace('T', ' ').replace('.000Z', 'Z')}
      </time>
    )
  }
  const diff = (d.getTime() - now) / 1000
  const [unit, secs] = UNITS.find(([, s]) => Math.abs(diff) >= s) ?? UNITS[3]
  return (
    <time dateTime={iso} title={iso} className="whitespace-nowrap">
      {rtf.format(Math.round(diff / secs), unit)}
    </time>
  )
}

export function Panel({ title, icon, children, note }: { title: string; icon?: ReactNode; note?: ReactNode; children: ReactNode }) {
  const id = `panel-${title.toLowerCase().replace(/\W+/g, '-')}`
  return (
    <section aria-labelledby={id} className="border-t border-zinc-800 pt-4">
      <h2 id={id} className="mb-1 flex items-center gap-2 text-sm font-semibold tracking-wide text-zinc-100 uppercase">
        {icon}
        {title}
      </h2>
      {note && <p className="mb-3 text-xs text-zinc-500">{note}</p>}
      {children}
    </section>
  )
}
