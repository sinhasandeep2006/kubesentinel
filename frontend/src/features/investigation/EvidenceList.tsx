import { CircleOff, Eye, TriangleAlert } from 'lucide-react'
import type { ReactNode } from 'react'
import type {
  DetectorSignalPayload,
  Evidence,
  KubeEvent,
  KubernetesEventsPayload,
  MetricsPayload,
  PodStatusPayload,
  PreviousLogsPayload,
  RestartSignalPayload,
  TerminationPayload,
} from '../../api/types'
import { Time } from '../../components/ui'

/** A field the backend did not report is shown as such, never as 0 / false. */
function V({ v }: { v: unknown }) {
  if (v === undefined || v === null || v === '') return <em className="text-zinc-500 not-italic">not reported</em>
  return <span className="font-mono text-zinc-100">{String(v)}</span>
}

function Fields({ rows }: { rows: [string, unknown][] }) {
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 text-xs">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-zinc-500">{k}</dt>
          <dd>
            <V v={v} />
          </dd>
        </div>
      ))}
    </dl>
  )
}

function Raw({ value }: { value: unknown }) {
  return (
    <pre className="max-h-64 overflow-auto rounded bg-zinc-950 p-2 font-mono text-xs text-zinc-300">
      {JSON.stringify(value, null, 2)}
    </pre>
  )
}

function Payload({ e }: { e: Evidence }): ReactNode {
  const p = e.payload ?? {}
  switch (e.type) {
    case 'restart_signal': {
      const r = p as Partial<RestartSignalPayload>
      return (
        <Fields
          rows={[
            ['restart count', r.restart_count],
            ['restarts in window', r.restart_delta],
            ['window', r.window_seconds != null ? `${r.window_seconds}s` : undefined],
            ['waiting reason', r.waiting_reason],
          ]}
        />
      )
    }
    case 'detector_signal': {
      const d = p as Partial<DetectorSignalPayload>
      return (
        <Fields
          rows={[
            ['detector', d.detector],
            ['reason', d.reason],
            ['message', d.message],
            ['waiting reason', d.waiting_reason],
            ['restart count', d.restart_count],
          ]}
        />
      )
    }
    case 'termination': {
      const t = p as Partial<TerminationPayload>
      return (
        <Fields
          rows={[
            ['reason', t.reason],
            ['exit code', t.exit_code],
            ['finished at', t.finished_at],
          ]}
        />
      )
    }
    case 'pod_status': {
      const s = p as Partial<PodStatusPayload>
      return (
        <Fields
          rows={[
            ['phase', s.phase],
            ['ready', s.ready],
            ['running', s.running],
            ['restart count', s.restart_count],
          ]}
        />
      )
    }
    case 'kubernetes_events': {
      const events = (p as Partial<KubernetesEventsPayload>).events
      if (!Array.isArray(events)) return <Raw value={p} />
      if (events.length === 0) {
        return <p className="text-xs text-zinc-500">Retrieved successfully; no events were recorded.</p>
      }
      return (
        <ul className="space-y-1 text-xs">
          {events.map((ev: KubeEvent, i) => (
            <li key={i} className="flex flex-wrap gap-x-2">
              <span className={ev.type === 'Warning' ? 'text-amber-300' : 'text-zinc-400'}>{ev.type}</span>
              <span className="font-mono text-zinc-100">{ev.reason}</span>
              {ev.count > 1 && <span className="text-zinc-500">x{ev.count}</span>}
              <span className="text-zinc-300">{ev.message}</span>
              {ev.last_seen && (
                <span className="text-zinc-500">
                  <Time value={ev.last_seen} />
                </span>
              )}
            </li>
          ))}
        </ul>
      )
    }
    case 'metrics': {
      const m = p as Partial<MetricsPayload>
      const pct = (v: number | null | undefined) => (v == null ? undefined : `${Math.round(v * 100)}%`)
      const mib = (v: number | null | undefined) => (v == null ? undefined : `${(v / 1048576).toFixed(1)} MiB`)
      return (
        <Fields
          rows={[
            ['scope', m.scope === 'pod' ? 'pod (no per-container series)' : m.scope],
            ['window', m.window_seconds != null ? `${m.window_seconds}s` : undefined],
            ['peak memory', mib(m.memory_max_bytes)],
            ['memory limit', m.memory_limit_bytes === null ? 'no limit or not reported' : mib(m.memory_limit_bytes)],
            ['peak / limit', pct(m.memory_ratio)],
            ['CPU throttled', pct(m.cpu_throttled_ratio)],
          ]}
        />
      )
    }
    case 'previous_logs': {
      const l = p as Partial<PreviousLogsPayload>
      if (!Array.isArray(l.lines)) return <Raw value={p} />
      if (l.lines.length === 0) {
        return <p className="text-xs text-zinc-500">Retrieved successfully; the previous container wrote no log lines.</p>
      }
      return (
        <>
          <pre className="max-h-64 overflow-auto rounded bg-zinc-950 p-2 font-mono text-xs leading-relaxed text-zinc-300">
            {l.lines.join('\n')}
          </pre>
          {l.truncated && <p className="mt-1 text-xs text-zinc-500">Truncated: only the last lines were collected.</p>}
        </>
      )
    }
    default:
      return <Raw value={p} />
  }
}

function Unavailable({ e }: { e: Evidence }) {
  const isError = e.availability === 'error'
  const Icon = isError ? TriangleAlert : CircleOff
  return (
    <p
      role="note"
      className={`flex items-start gap-2 rounded border border-dashed px-2 py-1.5 text-xs ${
        isError ? 'border-red-800 text-red-300' : 'border-amber-800 text-amber-300'
      }`}
    >
      <Icon aria-hidden className="mt-0.5 size-3.5 shrink-0" />
      <span>
        <strong>{isError ? 'Retrieval error' : 'Data unavailable'}</strong>
        {': '}
        {e.detail || 'no reason given by the backend'}. This is missing data, not a healthy or zero value.
      </span>
    </p>
  )
}

export function EvidenceList({ items }: { items: Evidence[] }) {
  return (
    <ul className="space-y-3">
      {items.map((e) => (
        <li
          key={e.id}
          id={`evidence-${e.id}`}
          className="scroll-mt-4 rounded border border-zinc-700 bg-zinc-900/60 p-3 target:border-sky-500"
        >
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="flex items-center gap-1 rounded bg-sky-950/60 px-1.5 py-0.5 font-medium text-sky-300 uppercase">
              <Eye aria-hidden className="size-3" /> Observed
            </span>
            <span className="font-mono text-zinc-300">{e.type}</span>
            <span className="font-mono text-zinc-500">#{e.id}</span>
            <span className="ml-auto">
              <Time value={e.observed_at} absolute />
            </span>
          </div>
          <p className="mt-1.5 text-sm text-zinc-100">{e.summary}</p>
          <p className="mt-0.5 text-xs text-zinc-500">
            source <span className="font-mono text-zinc-400">{e.source}</span> · resource{' '}
            <span className="font-mono break-all text-zinc-400">{e.resource}</span>
          </p>
          <div className="mt-2">{e.availability !== 'available' ? <Unavailable e={e} /> : <Payload e={e} />}</div>
        </li>
      ))}
    </ul>
  )
}
