import { ShieldAlert } from 'lucide-react'
import { Link, Outlet } from 'react-router'

export function AppLayout() {
  return (
    <div className="min-h-screen">
      <header className="border-b border-zinc-800 bg-zinc-900/80">
        <nav aria-label="Main" className="mx-auto flex max-w-screen-2xl items-center gap-6 px-4 py-2.5">
          <Link to="/" className="flex items-center gap-2 rounded font-semibold text-zinc-100">
            <ShieldAlert aria-hidden className="size-5 text-zinc-400" />
            KubeSentinel
          </Link>
          <Link to="/" className="rounded text-sm text-zinc-400 hover:text-zinc-100">
            Incident queue
          </Link>
          <span className="ml-auto text-xs text-zinc-500">read-only advisor</span>
        </nav>
      </header>
      <main className="mx-auto max-w-screen-2xl px-4 py-5">
        <Outlet />
      </main>
    </div>
  )
}
