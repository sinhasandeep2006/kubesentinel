import { Link } from 'react-router'

export function NotFoundPage() {
  return (
    <div className="py-10 text-center">
      <h1 className="text-lg font-semibold text-zinc-100">Page not found</h1>
      <Link to="/" className="mt-2 inline-block rounded text-sm text-sky-400 underline">
        Back to the incident queue
      </Link>
    </div>
  )
}
