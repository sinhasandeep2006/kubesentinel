import { Link, useNavigate } from 'react-router'
import type { Incident } from '../../api/types'
import { SeverityBadge, StatusBadge, Time } from '../../components/ui'

export function IncidentTable({ incidents }: { incidents: Incident[] }) {
  const navigate = useNavigate()
  return (
    <div className="overflow-x-auto rounded border border-zinc-800">
      <table className="w-full text-left text-sm">
        <caption className="sr-only">Incidents, newest first</caption>
        <thead className="bg-zinc-900 text-xs text-zinc-400 uppercase">
          <tr>
            <th scope="col" className="px-3 py-2 font-medium">Severity</th>
            <th scope="col" className="px-3 py-2 font-medium">Status</th>
            <th scope="col" className="px-3 py-2 font-medium">Workload</th>
            <th scope="col" className="px-3 py-2 font-medium">Pod / container</th>
            <th scope="col" className="px-3 py-2 font-medium">Summary</th>
            <th scope="col" className="px-3 py-2 font-medium">Detected</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-zinc-800/80">
          {incidents.map((i) => (
            <tr
              key={i.id}
              onClick={() => navigate(`/incidents/${i.id}`)}
              className="cursor-pointer hover:bg-zinc-900/70"
            >
              <td className="px-3 py-2"><SeverityBadge severity={i.severity} /></td>
              <td className="px-3 py-2"><StatusBadge status={i.status} /></td>
              <td className="px-3 py-2">
                <Link
                  to={`/incidents/${i.id}`}
                  onClick={(e) => e.stopPropagation()}
                  className="rounded font-mono text-xs text-zinc-100 underline-offset-2 hover:underline"
                >
                  {i.workload_kind}/{i.workload_name}
                </Link>
                <div className="font-mono text-xs text-zinc-500">{i.namespace}</div>
              </td>
              <td className="px-3 py-2 font-mono text-xs text-zinc-300">
                {i.pod}
                <span className="text-zinc-500"> / {i.container}</span>
              </td>
              <td className="px-3 py-2 text-zinc-300">{i.summary}</td>
              <td className="px-3 py-2 text-zinc-400"><Time value={i.detected_at} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
