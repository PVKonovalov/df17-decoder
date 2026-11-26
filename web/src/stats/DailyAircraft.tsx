import { useMemo } from 'react'
import type { DailyRow, DailyTotals, NewAircraft } from '../api/stats'
import { linkClick } from '../route'
import { Chart } from './Chart'
import { dailyOption, dayLabel, percentNew, todayAndYesterday, zoneNote } from './daily'
import type { ChartClick } from './echarts'
import type { ChartTheme } from './theme'
import { useJson, type Fetched } from './useJson'

export const NEW_LIMIT = 200
export const LEARNING_DAYS = 7 // history.LearningDays

function Tile({ label, row, partial }: { label: string; row: DailyRow | undefined; partial?: boolean }) {
  const pct = row ? percentNew(row) : null
  return (
    <div className="daily-tile" aria-label={label}>
      <div className="daily-tile-label">
        {label}
        {partial && <span className="muted"> · so far</span>}
      </div>
      {row === undefined ? (
        <div className="daily-tile-value">—</div>
      ) : (
        <>
          <div className="daily-tile-value">
            <span title="Different aircraft (ICAO addresses)">{row.aircraft.toLocaleString('en-US')}</span>
            <span className="daily-tile-sep">aircraft</span>
            <span title="Seen for the first time ever">{row.new.toLocaleString('en-US')}</span>
            <span className="daily-tile-sep">new</span>
            <span>{pct === null ? '—' : `${String(pct)}%`}</span>
          </div>
          <div className="daily-tile-split muted">
            ADS-B {row.adsb} · Mode-S {row.modeS} · new ADS-B {row.newAdsb} · new Mode-S {row.newModeS}
            {row.learning && ' · learning period'}
          </div>
        </>
      )}
    </div>
  )
}

/** DailyTiles shows today (so far) and yesterday from the daily totals. */
export function DailyTiles({ daily }: { daily: Fetched<DailyTotals> }) {
  if (daily.status !== 'ok') {
    return null
  }
  const { today, yesterday } = todayAndYesterday(daily.data.days)
  return (
    <section className="daily-tiles" aria-label="Aircraft today and yesterday">
      <Tile label="Today" row={today} partial />
      <Tile label="Yesterday" row={yesterday} />
    </section>
  )
}

const timeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

/** historySearch opens the aircraft's history panel on the map. */
export function historySearch(icao: string): string {
  return `?history=${encodeURIComponent(icao)}`
}

/** NewList lists the aircraft first seen on day; each row opens its history on the map. */
export function NewList({ day, onClose }: { day: string; onClose: () => void }) {
  const list = useJson<NewAircraft[]>(`/api/stats/new?day=${day}&limit=${String(NEW_LIMIT)}`)
  return (
    <div className="daily-new" aria-label={`New on ${day}`}>
      <div className="daily-new-head">
        <h3>New on {dayLabel(day)}</h3>
        <button type="button" className="detail-close" aria-label="Close the list" onClick={onClose}>
          ×
        </button>
      </div>
      {list.status === 'loading' && <p className="stats-hint">Loading…</p>}
      {list.status === 'error' && <p className="stats-hint">Could not load: {list.message}</p>}
      {list.status === 'ok' && list.data.length === 0 && <p className="stats-hint">No new aircraft that day.</p>}
      {list.status === 'ok' && list.data.length > 0 && (
        <>
          <p className="stats-hint">
            {list.data.length === NEW_LIMIT ? `The first ${String(NEW_LIMIT)}` : list.data.length} seen for the first time.
            Click a row to open its history on the map.
          </p>
          <ul className="notable-list">
            {list.data.map((a) => {
              const search = historySearch(a.icao)
              return (
                <li key={a.icao}>
                  <a href={`/${search}`} onClick={linkClick('/', search)}>
                    <span className={a.adsb ? 'tag tag-adsb' : 'tag tag-modes'}>{a.adsb ? 'ADS-B' : 'Mode-S'}</span>
                    <time dateTime={a.firstSeen}>{timeFmt.format(new Date(a.firstSeen))}</time>
                    <span className="notable-icao">{a.icao}</span>
                    <span>{a.callsign ?? ''}</span>
                    <span className="muted">{a.country ?? ''}</span>
                  </a>
                </li>
              )
            })}
          </ul>
        </>
      )}
    </div>
  )
}

/** dailyNote is the text instead of the chart: loading, disabled, failed or no days yet. */
export function dailyNote(daily: Fetched<DailyTotals>, off: string): string | null {
  switch (daily.status) {
    case 'loading':
      return 'Loading…'
    case 'disabled':
      return off
    case 'error':
      return `Could not load: ${daily.message}`
    case 'ok':
      return daily.data.days.length === 0 ? 'No data yet: daily totals start after the first full hour.' : null
  }
}

/** DailyChart is "Aircraft per day": a click on a day opens its new aircraft under the chart. */
export function DailyChart({
  daily,
  theme,
  browserTz,
  off,
  day,
  onDay,
}: {
  daily: Fetched<DailyTotals>
  theme: ChartTheme
  browserTz: string
  off: string
  day: string | null
  onDay: (day: string | null) => void
}) {
  const data = daily.status === 'ok' ? daily.data : null
  const note = dailyNote(daily, off)
  const zone = data ? zoneNote(data.timezone, browserTz) : null
  const option = useMemo(() => (note === null && data ? dailyOption(data, theme, browserTz) : null), [note, data, theme, browserTz])
  return (
    <>
      <Chart
        title="Aircraft per day"
        hint={
          <>
            Different aircraft (ICAO addresses) heard each day: new ones, seen that day for the first time since
            recording began, and returning ones; the line is the share of new. Hatched days are the learning period,
            the first {LEARNING_DAYS} days of recording, when nearly everything is new; today is lighter. Click a day for its new
            aircraft.{zone === null ? '' : ` ${zone}`}
          </>
        }
        option={option}
        note={note}
        onClick={(p: ChartClick) => {
          onDay(data?.days[p.dataIndex]?.day ?? null)
        }}
      />
      {day !== null && (
        <section className="stats-card" aria-label="New aircraft of the day">
          <NewList
            day={day}
            onClose={() => {
              onDay(null)
            }}
          />
        </section>
      )}
    </>
  )
}
