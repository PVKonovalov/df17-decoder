import { useEffect, useState } from 'react'

/** Fetched is a JSON response: loading at first, disabled on 503 (the feature is off), error on other failures. */
export type Fetched<T> =
  | { status: 'loading' }
  | { status: 'ok'; data: T }
  | { status: 'disabled' }
  | { status: 'error'; message: string }

/**
 * useJson fetches url, again whenever url or tick changes, keeping the last result on screen while it reloads.
 * null skips the fetch.
 */
export function useJson<T>(url: string | null, tick = 0): Fetched<T> {
  const [res, setRes] = useState<Fetched<T>>({ status: 'loading' })
  useEffect(() => {
    if (url === null) {
      return
    }
    const ctl = new AbortController()
    fetch(url, { signal: ctl.signal })
      .then(async (r) => {
        if (r.status === 503) {
          setRes({ status: 'disabled' })
        } else if (!r.ok) {
          const body = (await r.json().catch(() => null)) as { error?: string } | null
          setRes({ status: 'error', message: body?.error ?? `HTTP ${r.status}` })
        } else {
          setRes({ status: 'ok', data: (await r.json()) as T })
        }
      })
      .catch((e: unknown) => {
        if (!ctl.signal.aborted) {
          setRes({ status: 'error', message: e instanceof Error ? e.message : 'request failed' })
        }
      })
    return () => {
      ctl.abort()
    }
  }, [url, tick])
  return res
}
