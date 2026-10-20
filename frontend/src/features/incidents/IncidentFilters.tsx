import { INCIDENT_STATUSES, SEVERITIES, type IncidentFilters as Filters } from '../../api/types'

const selectCls =
  'rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-200 hover:border-zinc-500'

interface Props {
  filters: Filters
  set: (key: keyof Filters, value: string) => void
}

export function IncidentFilters({ filters, set }: Props) {
  return (
    <search aria-label="Filter incidents">
      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          set('namespace', String(new FormData(e.currentTarget).get('namespace') ?? '').trim())
        }}
      >
        <label className="flex flex-col gap-1 text-xs text-zinc-400">
          Status
          <select className={selectCls} value={filters.status} onChange={(e) => set('status', e.target.value)}>
            <option value="">any</option>
            {INCIDENT_STATUSES.map((s) => (
              <option key={s} value={s}>
                {s.replace('_', ' ')}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-zinc-400">
          Severity
          <select className={selectCls} value={filters.severity} onChange={(e) => set('severity', e.target.value)}>
            <option value="">any</option>
            {SEVERITIES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-zinc-400">
          Namespace
          <input
            key={filters.namespace}
            name="namespace"
            defaultValue={filters.namespace}
            placeholder="any"
            spellCheck={false}
            className={`${selectCls} w-48 font-mono`}
          />
        </label>
        <button type="submit" className="rounded border border-zinc-600 bg-zinc-800 px-3 py-1 text-sm text-zinc-100 hover:bg-zinc-700">
          Apply
        </button>
      </form>
    </search>
  )
}
