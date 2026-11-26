import { useEffect, useId, useRef, useState, type Dispatch } from 'react'
import type { AircraftMatch } from '../api/history'
import type { Aircraft } from '../api/types'
import { isTyping } from '../deck/tape'
import type { Action } from '../state/reducer'

export const SEARCH_DEBOUNCE_MS = 250
export const SEARCH_LIMIT = 20

/** normalizeQuery trims and upper-cases a search text, as the server does. */
export function normalizeQuery(q: string): string {
  return q.trim().toUpperCase()
}

const HEX_RE = /^[0-9A-F]{1,6}$/

/**
 * matchesQuery mirrors history.MatchesSearch: a 1–6 digit hex q matches the address by prefix; any q matches the
 * callsign by prefix, or by substring from 3 characters. q must be normalized.
 */
export function matchesQuery(q: string, icao: string, callsign: string | undefined): boolean {
  if (q === '') {
    return false
  }
  if (HEX_RE.test(q) && icao.startsWith(q)) {
    return true
  }
  if (callsign === undefined || callsign === '') {
    return false
  }
  return callsign.startsWith(q) || (q.length >= 3 && callsign.includes(q))
}

/** liveMatches searches the aircraft on the map, newest first, as search results without history counts. */
export function liveMatches(aircraft: ReadonlyMap<string, Aircraft>, q: string, limit = SEARCH_LIMIT): AircraftMatch[] {
  const n = normalizeQuery(q)
  return [...aircraft.values()]
    .filter((a) => matchesQuery(n, a.icao, a.callsign))
    .sort((a, b) => b.lastSeen.localeCompare(a.lastSeen) || a.icao.localeCompare(b.icao))
    .slice(0, limit)
    .map((a) => ({
      icao: a.icao,
      ...(a.callsign !== undefined && { lastCallsign: a.callsign }),
      callsigns: a.callsign === undefined ? [] : [a.callsign],
      ...(a.country !== undefined && { country: a.country }),
      lastSeen: a.lastSeen,
      flights: 0,
      adsb: a.adsb,
      live: true,
    }))
}

/** searchUrl is the server search for q. */
export function searchUrl(q: string): string {
  return `/api/history/search?q=${encodeURIComponent(normalizeQuery(q))}&limit=${String(SEARCH_LIMIT)}`
}

const lastSeenText = (iso: string) => {
  const d = new Date(iso)
  const today = new Date().toDateString() === d.toDateString()
  return today ? d.toLocaleTimeString() : d.toLocaleString()
}

/**
 * useSearch returns the results for text after a SEARCH_DEBOUNCE_MS pause: from the server's history when history
 * is on, else from the aircraft on the map. null while nothing was searched yet.
 */
function useSearch(text: string, history: boolean, aircraft: ReadonlyMap<string, Aircraft>) {
  const [query, setQuery] = useState('')
  const [found, setFound] = useState<{ q: string; rows: AircraftMatch[] } | null>(null)
  useEffect(() => {
    const t = setTimeout(() => {
      setQuery(normalizeQuery(text))
    }, SEARCH_DEBOUNCE_MS)
    return () => {
      clearTimeout(t)
    }
  }, [text])
  useEffect(() => {
    if (!history || query === '') {
      return
    }
    const ctl = new AbortController()
    fetch(searchUrl(query), { signal: ctl.signal })
      .then((r) => (r.ok ? (r.json() as Promise<AircraftMatch[]>) : []))
      .then((rows) => {
        setFound({ q: query, rows })
      })
      .catch(() => undefined)
    return () => {
      ctl.abort()
    }
  }, [query, history])
  if (query === '') {
    return null
  }
  if (!history) {
    return liveMatches(aircraft, query)
  }
  return found?.q === query ? found.rows : null
}

/**
 * SearchBox finds aircraft by ICAO address or callsign: with history, every aircraft ever seen (live ones first),
 * else the aircraft on the map. Arrow keys move, Enter picks, Esc closes, and "/" focuses it from anywhere outside
 * a form field. Picking dispatches showHistory: an aircraft on the map is selected, any other opens its history.
 */
export function SearchBox({
  aircraft,
  history,
  dispatch,
}: {
  aircraft: ReadonlyMap<string, Aircraft>
  history: boolean
  dispatch: Dispatch<Action>
}) {
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const listId = useId()
  const rows = useSearch(text, history, aircraft)
  const shown = open && rows !== null && text.trim() !== ''

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey || isTyping(e.target)) {
        return
      }
      e.preventDefault()
      input.current?.focus()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
    }
  }, [])

  const pick = (m: AircraftMatch) => {
    dispatch({ type: 'showHistory', icao: m.icao })
    setOpen(false)
    input.current?.blur()
  }
  const count = rows?.length ?? 0
  return (
    <div className="search">
      <input
        ref={input}
        type="search"
        placeholder={history ? 'Search aircraft  /' : 'Search the map  /'}
        aria-label={history ? 'Search aircraft by ICAO or callsign' : 'Search the aircraft on the map by ICAO or callsign'}
        role="combobox"
        aria-expanded={shown}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={shown && count > 0 ? `${listId}-${String(Math.min(active, count - 1))}` : undefined}
        maxLength={16}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          setActive(0)
          setOpen(true)
        }}
        onFocus={() => {
          setOpen(true)
        }}
        onBlur={() => {
          setOpen(false)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            if (shown) {
              e.preventDefault()
              setOpen(false)
            } else {
              input.current?.blur()
            }
          } else if (e.key === 'ArrowDown' && count > 0) {
            e.preventDefault()
            setOpen(true)
            setActive((i) => (Math.min(i, count - 1) + 1) % count)
          } else if (e.key === 'ArrowUp' && count > 0) {
            e.preventDefault()
            setActive((i) => (Math.min(i, count - 1) - 1 + count) % count)
          } else if (e.key === 'Enter' && shown && count > 0) {
            e.preventDefault()
            const m = rows[Math.min(active, count - 1)]
            if (m !== undefined) {
              pick(m)
            }
          }
        }}
      />
      {shown && (
        <ul className="search-results" id={listId} role="listbox" aria-label="Aircraft found">
          {count === 0 && <li className="muted">{history ? 'No aircraft' : 'No aircraft on the map'}</li>}
          {rows.map((m, i) => (
            <li
              key={m.icao}
              id={`${listId}-${String(i)}`}
              role="option"
              aria-selected={i === Math.min(active, count - 1)}
              className={i === Math.min(active, count - 1) ? 'active' : undefined}
              onMouseDown={(e) => {
                e.preventDefault() // keep the focus until the click picks
              }}
              onMouseEnter={() => {
                setActive(i)
              }}
              onClick={() => {
                pick(m)
              }}
            >
              <span className="search-main">
                <strong>{m.icao}</strong>
                {m.lastCallsign !== undefined && <span>{m.lastCallsign}</span>}
                {m.live ? <span className="search-live">live</span> : <span className="muted">{lastSeenText(m.lastSeen)}</span>}
              </span>
              <span className="search-sub muted">
                {[
                  m.callsigns.filter((c) => c !== m.lastCallsign).join(', '),
                  m.country,
                  history && m.flights > 0 ? `${String(m.flights)} flight${m.flights === 1 ? '' : 's'}` : '',
                ]
                  .filter((v) => v !== undefined && v !== '')
                  .join(' · ')}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
