import { CircleAlert, CircleCheck, CircleMinus } from 'lucide-react'
import { useCapabilities } from '../../api/client'
import type { SourceStatus } from '../../api/types'
import { QueryState } from '../../components/ui'

const STYLE: Record<SourceStatus, { cls: string; Icon: typeof CircleCheck }> = {
  available: { cls: 'text-emerald-400', Icon: CircleCheck },
  degraded: { cls: 'text-amber-400', Icon: CircleAlert },
  unavailable: { cls: 'text-zinc-500', Icon: CircleMinus },
}

/** Which telemetry sources the backend can reach; explains why a diagnosis may be incomplete. */
export function CapabilitiesStrip() {
  const query = useCapabilities()
  return (
    <section aria-label="Data sources" className="rounded border border-zinc-800 bg-zinc-900/50 px-3 py-2">
      <QueryState query={query} label="data sources" isEmpty={(d) => !d.sources?.length} empty="Backend reported no data sources.">
        {(caps) => {
          const missing = caps.sources.filter((s) => s.status !== 'available')
          return (
            <>
              <ul className="flex flex-wrap gap-x-5 gap-y-1 text-xs">
                <li className="text-zinc-500 uppercase">Data sources</li>
                {caps.sources.map((s) => {
                  const { cls, Icon } = STYLE[s.status] ?? STYLE.unavailable
                  return (
                    <li key={s.name} className="flex items-center gap-1.5" title={s.detail || undefined}>
                      <Icon aria-hidden className={`size-3.5 ${cls}`} />
                      <span className="font-mono text-zinc-200">{s.name}</span>
                      <span className={cls}>{s.status}</span>
                      {s.detail && <span className="text-zinc-500">· {s.detail}</span>}
                    </li>
                  )
                })}
              </ul>
              {missing.length > 0 && (
                <p className="mt-1 text-xs text-amber-300/80">
                  Diagnosis may be incomplete: {missing.map((s) => s.name).join(', ')}{' '}
                  {missing.length === 1 ? 'is' : 'are'} not fully available, so evidence from{' '}
                  {missing.length === 1 ? 'it' : 'them'} could not be collected.
                </p>
              )}
            </>
          )
        }}
      </QueryState>
    </section>
  )
}
