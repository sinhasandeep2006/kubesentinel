import { useSearchParams } from 'react-router'
import { useIncidents } from '../api/client'
import type { IncidentFilters as Filters } from '../api/types'
import { QueryState } from '../components/ui'
import { IncidentFilters } from '../features/incidents/IncidentFilters'
import { IncidentTable } from '../features/incidents/IncidentTable'

export function IncidentQueuePage() {
  // Filters live in the URL so a filtered queue is shareable and survives reload.
  const [params, setParams] = useSearchParams()
  const filters: Filters = {
    status: params.get('status') ?? '',
    severity: params.get('severity') ?? '',
    namespace: params.get('namespace') ?? '',
  }
  const set = (key: keyof Filters, value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    setParams(next, { replace: true })
  }
  const query = useIncidents(filters)
  const filtered = Object.values(filters).some(Boolean)
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <h1 className="text-lg font-semibold text-zinc-100">Incident queue</h1>
        <IncidentFilters filters={filters} set={set} />
      </div>
      <QueryState
        query={query}
        label="incidents"
        isEmpty={(d) => d.length === 0}
        empty={filtered ? 'No incidents match these filters.' : 'No incidents detected.'}
      >
        {(incidents) => <IncidentTable incidents={incidents} />}
      </QueryState>
    </div>
  )
}
