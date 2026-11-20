import { useSyncExternalStore, type MouseEvent } from 'react'

export const STATS_PATH = '/stats'
const NAVIGATE = 'df17:navigate'

/** isStatsPath reports whether pathname is the Statistics page. */
export function isStatsPath(pathname: string): boolean {
  return pathname === STATS_PATH || pathname === `${STATS_PATH}/`
}

/** navigate pushes path with search (by default the current query, kept intact) and re-renders the page. */
export function navigate(path: string, search = window.location.search) {
  window.history.pushState(null, '', `${path}${search}`)
  window.dispatchEvent(new Event(NAVIGATE))
}

/** linkClick makes a plain left click on an <a> navigate in place; modified clicks keep the browser's behaviour. */
export function linkClick(path: string, search?: string) {
  return (e: MouseEvent<HTMLAnchorElement>) => {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
      return
    }
    e.preventDefault()
    navigate(path, search)
  }
}

function subscribe(onChange: () => void) {
  window.addEventListener('popstate', onChange)
  window.addEventListener(NAVIGATE, onChange)
  return () => {
    window.removeEventListener('popstate', onChange)
    window.removeEventListener(NAVIGATE, onChange)
  }
}

/** useLocation returns the current pathname and query, updated on navigate and the browser's back/forward. */
export function useLocation(): string {
  return useSyncExternalStore(subscribe, () => window.location.pathname + window.location.search)
}
