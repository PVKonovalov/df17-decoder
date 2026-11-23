import { useEffect, useMemo, useState } from 'react'
import type { AltitudeBand, CountryCount, HourRow, Notable, OperatorCount, RangeDay, StorageReport } from '../api/stats'
import { linkClick, navigate } from '../route'
import { gnssShare, heatmap, momentSearch, topWithOther } from './aggregate'
import { Chart } from './Chart'
import { altitudeOption, healthOption, heatmapOption, hourlyOption, qualityOption, rangeOption, rankingOption } from './charts'
import type { ChartClick } from './echarts'
import { parseRange, rangeSearch } from './range'
import { NotableList } from './NotableList'
import { RangePicker } from './RangePicker'
import { StoragePanel } from './StoragePanel'
import { useChartTheme } from './theme'
import { useJson, type Fetched } from './useJson'

const REFRESH_MS = 60_000
const TOP = 15
const HISTORY_OFF = 'The traffic charts need the flight history: set history.enabled: true in the config.'

/** note returns the text shown instead of a chart: loading, disabled, failed, or empty; null when there is data. */
function note<T>(f: Fetched<T>, empty: (d: T) => string | null): string | null {
  switch (f.status) {
    case 'loading':
      return 'Loading…'
    case 'disabled':
      return HISTORY_OFF
    case 'error':
      return `Could not load: ${f.message}`
    case 'ok':
      return empty(f.data)
  }
}

function openMoment(hour: string | undefined) {
  if (hour !== undefined) {
    navigate('/', momentSearch(hour))
  }
}

/** StatsPage is /stats: the storage panel and the traffic charts over a range kept in the URL. */
export default function StatsPage() {
  const [search, setSearch] = useState(window.location.search)
  const [now, setNow] = useState(() => new Date())
  const range = useMemo(() => parseRange(search, now), [search, now])
  const tz = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone, [])
  const theme = useChartTheme()

  useEffect(() => {
    document.title = 'Statistics · df17-decoder'
    const onPop = () => {
      setSearch(window.location.search)
    }
    window.addEventListener('popstate', onPop)
    return () => {
      window.removeEventListener('popstate', onPop)
      document.title = 'df17-decoder'
    }
  }, [])
  useEffect(() => {
    const id = setInterval(() => {
      setNow(new Date())
    }, REFRESH_MS)
    return () => {
      clearInterval(id)
    }
  }, [])

  // a live range moves with the clock, a past one stays put
  const q = `from=${range.from.toISOString()}&to=${range.to.toISOString()}`
  const tick = range.live ? now.getTime() : 0
  const storage = useJson<StorageReport>('/api/storage', now.getTime())
  const hourly = useJson<HourRow[]>(`/api/stats/hourly?${q}`, tick)
  const countries = useJson<CountryCount[]>(`/api/stats/countries?${q}&limit=500`, tick)
  const days = useJson<RangeDay[]>(`/api/stats/range-daily?${q}&tz=${encodeURIComponent(tz)}`, tick)
  const operators = useJson<OperatorCount[]>(`/api/stats/operators?${q}&limit=500`, tick)
  const altitude = useJson<AltitudeBand[]>(`/api/stats/altitude?${q}`, tick)
  const notable = useJson<Notable>(`/api/stats/notable?${q}`, tick)

  const rows = useMemo(() => (hourly.status === 'ok' ? hourly.data : []), [hourly])
  const hm = useMemo(() => heatmap(rows, tz), [rows, tz])
  const gnssHm = useMemo(() => heatmap(rows, tz, gnssShare), [rows, tz])
  const hasQuality = rows.some((r) => r.adsbPositionOk !== undefined)
  const top = useMemo(
    () => (countries.status === 'ok' ? topWithOther(countries.data.map((c) => ({ name: c.country, aircraftHours: c.aircraftHours })), TOP) : []),
    [countries],
  )
  const topOps = useMemo(
    () => (operators.status === 'ok' ? topWithOther(operators.data.map((o) => ({ name: o.prefix, aircraftHours: o.aircraftHours })), TOP) : []),
    [operators],
  )
  const bands = useMemo(() => (altitude.status === 'ok' ? altitude.data : []), [altitude])
  const hasHealth = rows.some((r) => r.frames !== undefined)
  const hasAltitude = bands.some((b) => b.positions > 0)
  const dayList = useMemo(() => (days.status === 'ok' ? days.data : []), [days])

  const hourlyNote = note(hourly, (d) => (d.length === 0 ? 'No data yet: rollups start after the first full hour.' : null))
  const options = useMemo(
    () => ({
      hourly: hourlyNote === null ? hourlyOption(rows, theme, tz) : null,
      heatmap: hm.cells.length > 0 ? heatmapOption(hm, theme) : null,
      quality: hasQuality ? qualityOption(rows, theme, tz) : null,
      gnssHeatmap: gnssHm.cells.length > 0
        ? heatmapOption(gnssHm, theme, (v) => `${v}% of ADS-B aircraft lost or degraded their position`, [theme.heatLow, theme.bad])
        : null,
      countries: top.length > 0 ? rankingOption(top, theme, theme.adsb) : null,
      operators: topOps.length > 0 ? rankingOption(topOps, theme, theme.line) : null,
      altitude: hasAltitude ? altitudeOption(bands, theme) : null,
      health: hasHealth ? healthOption(rows, theme, tz) : null,
      range: dayList.length > 0 ? rangeOption(dayList, theme) : null,
    }),
    [hourlyNote, rows, hm, gnssHm, hasQuality, top, topOps, bands, hasAltitude, hasHealth, dayList, theme, tz],
  )

  const choose = (sel: Parameters<typeof rangeSearch>[1]) => {
    const s = rangeSearch(window.location.search, sel)
    window.history.replaceState(null, '', `${window.location.pathname}${s}`)
    setSearch(s)
    setNow(new Date())
  }

  return (
    <div className="stats-page">
      <header className="header">
        <div className="header-brand">
          <h1 className="header-title">df17-decoder</h1>
          <span className="muted">Statistics</span>
        </div>
        <RangePicker range={range} onChange={choose} />
        <a className="header-link" href="/" onClick={linkClick('/', '')}>
          ← Map
        </a>
      </header>
      <main className="stats-main">
        {storage.status === 'ok' ? (
          <StoragePanel report={storage.data} />
        ) : (
          <section className="stats-card stats-storage" aria-label="Storage">
            <h2>Storage</h2>
            <p className="stats-note">{note(storage, () => null) ?? ''}</p>
          </section>
        )}
        <Chart
          title="Aircraft per hour"
          hint="Distinct aircraft heard in each hour; the current hour is lighter. Click a bar to replay that hour."
          option={options.hourly}
          note={hourlyNote}
          onClick={(p: ChartClick) => {
            openMoment(rows[p.dataIndex]?.hour)
          }}
        />
        <div className="stats-grid">
          <Chart
            title="Hour × weekday"
            hint={`Average aircraft per hour, ${tz} time. Click a cell to replay its latest hour.`}
            option={options.heatmap}
            note={note(hourly, () => (hm.cells.length === 0 ? 'No full hours in this range yet.' : null))}
            height={260}
            onClick={(p: ChartClick) => {
              const [h, d] = p.value as [number, number]
              openMoment(hm.latest.get(`${d}-${h}`))
            }}
          />
          <Chart
            title="Countries"
            hint="Aircraft-hours by country of registration (each aircraft counts once per hour it was heard)."
            option={options.countries}
            note={note(countries, (d) => (d.length === 0 ? 'No aircraft in this range.' : null))}
            height={Math.max(260, top.length * 22 + 24)}
          />
        </div>
        <div className="stats-grid">
          <Chart
            title="Operators"
            hint="Aircraft-hours by airline designator: the first 3 letters of callsigns like KLM1023; other callsigns aren't counted."
            option={options.operators}
            note={note(operators, (d) => (d.length === 0 ? 'No airline callsigns in this range.' : null))}
            height={Math.max(260, topOps.length * 22 + 24)}
          />
          <Chart
            title="Altitude"
            hint="Stored positions per altitude band. Track points are kept about every 5 s, so this is close to time spent at each altitude."
            option={options.altitude}
            note={note(altitude, () => (hasAltitude ? null : 'No positions in this range.'))}
            height={Math.max(260, topOps.length * 22 + 24)}
          />
        </div>
        <Chart
          title="Max range per day"
          hint="The furthest position heard each day; the marker shows the best day. Compare antenna changes here."
          option={options.range}
          note={note(days, (d) => (d.length === 0 ? 'No positions in this range.' : null))}
          onClick={(p: ChartClick) => {
            openMoment(dayList[p.dataIndex]?.hour)
          }}
        />
        <Chart
          title="ADS-B position quality"
          hint="ADS-B aircraft per hour by what they reported about their own position, worst case in the hour: lost (TC 0, or no position for 30 s while other messages arrive), degraded (integrity NIC < 7 or accuracy NACp < 8, as under GNSS jamming), or no position received at all (often range or too few frames). The line is the share lost or degraded. Click a bar to replay the hour."
          option={options.quality}
          note={note(hourly, () => (hasQuality ? null : 'No position quality yet: it is recorded from this version on, for hours whose flights are still stored.'))}
          onClick={(p: ChartClick) => {
            openMoment(rows[p.dataIndex]?.hour)
          }}
        />
        <Chart
          title="GNSS problems × weekday"
          hint={`Share of ADS-B aircraft that lost or degraded their position, ${tz} time. Recurring cells point at scheduled jamming. Click a cell to replay its latest hour.`}
          option={options.gnssHeatmap}
          note={note(hourly, () => (gnssHm.cells.length === 0 ? 'No full hours with position quality in this range yet.' : null))}
          height={260}
          onClick={(p: ChartClick) => {
            const [h, d] = p.value as [number, number]
            openMoment(gnssHm.latest.get(`${d}-${h}`))
          }}
        />
        <Chart
          title="Receiver health"
          hint="Frames per hour, the share failing CRC, and minutes without a single frame (red). Hover for every counter. Click a bar to replay the hour."
          option={options.health}
          note={note(hourly, () =>
            hasHealth ? null : 'No receiver counters yet: they are recorded for each full hour the service runs through, not for backfilled hours.',
          )}
          onClick={(p: ChartClick) => {
            openMoment(rows[p.dataIndex]?.hour)
          }}
        />
        <section className="stats-card" aria-label="Notable aircraft">
          <h2>Notable aircraft</h2>
          {notable.status === 'ok' && notable.data.events.length > 0 ? (
            <NotableList notable={notable.data} />
          ) : (
            <p className="stats-note">{note(notable, () => 'No emergencies, position losses or new aircraft in this range.') ?? ''}</p>
          )}
        </section>
      </main>
    </div>
  )
}
