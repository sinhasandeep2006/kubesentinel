import { useQuery } from '@tanstack/react-query'
import type {
  Capabilities,
  Evidence,
  Hypothesis,
  Incident,
  IncidentFilters,
  ListResponse,
  Proposal,
  TimelineEntry,
  Verification,
} from './types'

export class ApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`/api/v1${path}`, { headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // Non-JSON error body: keep the status line.
    }
    throw new ApiError(res.status, message)
  }
  return (await res.json()) as T
}

const items = <T,>(path: string) => get<ListResponse<T>>(path).then((r) => r.items ?? [])

export const useCapabilities = () =>
  useQuery({ queryKey: ['capabilities'], queryFn: () => get<Capabilities>('/system/capabilities') })

export function useIncidents(filters: IncidentFilters) {
  const qs = new URLSearchParams(
    Object.entries(filters).filter((e): e is [string, string] => Boolean(e[1])),
  ).toString()
  return useQuery({
    queryKey: ['incidents', qs],
    queryFn: () => items<Incident>(`/incidents${qs ? `?${qs}` : ''}`),
  })
}

export const useIncident = (id: string) =>
  useQuery({ queryKey: ['incident', id], queryFn: () => get<Incident>(`/incidents/${id}`) })

function useIncidentList<T>(id: string, name: string) {
  return useQuery({
    queryKey: ['incident', id, name],
    queryFn: () => items<T>(`/incidents/${id}/${name}`),
  })
}

export const useEvidence = (id: string) => useIncidentList<Evidence>(id, 'evidence')
export const useHypotheses = (id: string) => useIncidentList<Hypothesis>(id, 'hypotheses')
export const useTimeline = (id: string) => useIncidentList<TimelineEntry>(id, 'timeline')
export const useVerification = (id: string) => useIncidentList<Verification>(id, 'verification')
export const useRemediations = (id: string) => useIncidentList<Proposal>(id, 'remediations')
