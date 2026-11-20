import { lazy, Suspense } from 'react'
import { App } from './App'
import { isStatsPath, useLocation } from './route'

const StatsPage = lazy(() => import('./stats/StatsPage'))

/** Root renders the Statistics page (a lazy chunk) on /stats and the live map everywhere else. */
export function Root() {
  useLocation()
  if (isStatsPath(window.location.pathname)) {
    return (
      <Suspense fallback={<p className="stats-note">Loading statistics…</p>}>
        <StatsPage />
      </Suspense>
    )
  }
  return <App />
}
