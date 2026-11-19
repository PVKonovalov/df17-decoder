import {
  createColumnHelper,
  rowSortingFeature,
  tableFeatures,
  useTable,
  type SortingState,
} from '@tanstack/react-table'
import { createContext, useContext, useEffect, useMemo, useRef, useState, type Dispatch } from 'react'
import type { Aircraft } from '../api/types'
import { aircraftAltitude, dataNow } from '../map/style'
import type { Action } from '../state/reducer'
import { parseUrl, replaceUrl } from '../state/url'
import { ageSeconds, fmtAgo, fmtVerticalRate, isEmergency, newAgeMs, visibleRows, type ColumnId, type Filter } from './rows'

const features = tableFeatures({ rowSortingFeature })
const helper = createColumnHelper<typeof features, Aircraft>()

/** Data-time "now" in ms, ticking every second, for the last-seen column. */
const NowContext = createContext(0)

const num = (v: number | undefined, digits = 0) => (v === undefined ? '' : v.toFixed(digits))

function LastSeen({ a }: { a: Aircraft }) {
  const now = useContext(NowContext)
  return <>{now === 0 ? '' : fmtAgo(ageSeconds(a, now))}</>
}

function IcaoCell({ a }: { a: Aircraft }) {
  return (
    <>
      <span className="mono">{a.icao}</span>
      {!a.hasPosition && <span className="badge">no pos</span>}
    </>
  )
}

const allColumns = helper.columns([
  helper.accessor('icao', { id: 'icao', header: 'ICAO', cell: ({ row }) => <IcaoCell a={row.original} /> }),
  helper.accessor('callsign', { id: 'callsign', header: 'Callsign', cell: ({ row }) => row.original.callsign ?? '' }),
  helper.accessor('squawk', { id: 'squawk', header: 'Squawk', cell: ({ row }) => row.original.squawk ?? '' }),
  helper.accessor((a) => aircraftAltitude(a), {
    id: 'altitude',
    header: 'Alt ft',
    cell: ({ row }) => {
      const a = row.original
      const alt = aircraftAltitude(a)
      return a.onGround === true ? 'GND' : alt === undefined ? '' : alt.toLocaleString('en-US')
    },
  }),
  helper.accessor('groundSpeed', { id: 'groundSpeed', header: 'GS kt', cell: ({ row }) => num(row.original.groundSpeed) }),
  helper.accessor('track', { id: 'track', header: 'Trk °', cell: ({ row }) => num(row.original.track) }),
  helper.accessor('verticalRate', {
    id: 'verticalRate',
    header: 'V/S ft/min',
    cell: ({ row }) => (row.original.verticalRate === undefined ? '' : fmtVerticalRate(row.original.verticalRate)),
  }),
  helper.accessor('distanceNm', { id: 'distance', header: 'Dist nm', cell: ({ row }) => num(row.original.distanceNm, 1) }),
  helper.accessor('bearing', { id: 'bearing', header: 'Brg °', cell: ({ row }) => num(row.original.bearing) }),
  helper.accessor('country', { id: 'country', header: 'Country', cell: ({ row }) => row.original.country ?? '' }),
  helper.accessor('messages', { id: 'messages', header: 'Msgs', cell: ({ row }) => row.original.messages }),
  helper.accessor('rssi', { id: 'rssi', header: 'RSSI dBFS', cell: ({ row }) => num(row.original.rssi, 1) }),
  helper.accessor('lastSeen', { id: 'lastSeen', header: 'Seen', cell: ({ row }) => <LastSeen a={row.original} /> }),
])
const columnsWithoutRssi = allColumns.filter((c) => c.id !== 'rssi')

const TEXT_COLUMNS = new Set<string>(['icao', 'callsign', 'squawk', 'country'])

/**
 * useDataNow returns the newest data time in the table plus the wall time since that snapshot. Snapshots
 * re-render every second anyway; the timer only ticks when they stop arriving.
 */
function useDataNow(aircraft: ReadonlyMap<string, Aircraft>): number {
  const [, setTick] = useState(0)
  const base = useMemo(() => ({ data: dataNow(aircraft.values()), wall: Date.now() }), [aircraft])
  const lastWall = useRef(base.wall)
  useEffect(() => {
    lastWall.current = base.wall
  }, [base])
  useEffect(() => {
    const id = setInterval(() => {
      if (Date.now() - lastWall.current >= 1000) {
        setTick((t) => t + 1)
      }
    }, 1000)
    return () => {
      clearInterval(id)
    }
  }, [])
  return base.data === 0 ? 0 : base.data + (Date.now() - base.wall)
}

interface Props {
  aircraft: ReadonlyMap<string, Aircraft>
  selected: string | null
  dispatch: Dispatch<Action>
}

/**
 * AircraftTable lists all aircraft, including Mode-S-only ones, with sorting, a text filter and a
 * positions-only toggle. Emergencies are pinned on top. Clicking a row selects it and pans the map.
 */
export function AircraftTable({ aircraft, selected, dispatch }: Props) {
  const [initial] = useState(() => parseUrl(window.location.search))
  const [filter, setFilter] = useState<Filter>(initial.filter)
  const [sorting, setSorting] = useState<SortingState>([initial.sort])
  const now = useDataNow(aircraft)
  const hasRssi = useMemo(() => [...aircraft.values()].some((a) => a.rssi !== undefined), [aircraft])

  const sort = sorting[0]
  const sortId = sort?.id as ColumnId | undefined
  const sortDesc = sort?.desc ?? false
  useEffect(() => {
    replaceUrl({ filter, ...(sortId === undefined ? {} : { sort: { id: sortId, desc: sortDesc } }) })
  }, [filter, sortId, sortDesc])
  const rows = visibleRows(aircraft.values(), filter, sort ? { id: sort.id as ColumnId, desc: sort.desc } : null, now)

  const table = useTable({
    features,
    columns: hasRssi ? allColumns : columnsWithoutRssi,
    data: rows,
    getRowId: (a) => a.icao,
    state: { sorting },
    onSortingChange: setSorting,
    manualSorting: true,
    enableMultiSort: false,
    enableSortingRemoval: false,
    sortDescFirst: false,
  })

  const bodyRef = useRef<HTMLTableSectionElement>(null)
  useEffect(() => {
    if (selected !== null) {
      bodyRef.current?.querySelector(`tr[data-icao="${selected}"]`)?.scrollIntoView({ block: 'nearest' })
    }
  }, [selected])

  return (
    <section className="table-pane" aria-label="Aircraft">
      <div className="table-filters">
        <input
          type="search"
          placeholder="Filter callsign, ICAO, squawk"
          aria-label="Filter"
          value={filter.text}
          onChange={(e) => {
            setFilter((f) => ({ ...f, text: e.target.value }))
          }}
        />
        <label>
          <input
            type="checkbox"
            checked={filter.withPosition}
            onChange={(e) => {
              setFilter((f) => ({ ...f, withPosition: e.target.checked }))
            }}
          />
          with position
        </label>
        <span className="muted">{rows.length} shown</span>
      </div>
      <div className="table-scroll">
        <NowContext value={now}>
          <table className="ac-table">
            <thead>
              {table.getHeaderGroups().map((group) => (
                <tr key={group.id}>
                  {group.headers.map((header) => {
                    const dir = header.column.getIsSorted()
                    return (
                      <th
                        key={header.id}
                        className={TEXT_COLUMNS.has(header.column.id) ? '' : 'num'}
                        aria-sort={dir === 'asc' ? 'ascending' : dir === 'desc' ? 'descending' : 'none'}
                        onClick={header.column.getToggleSortingHandler()}
                      >
                        <table.FlexRender header={header} />
                        <span className="sort-ind">{dir === 'asc' ? '▲' : dir === 'desc' ? '▼' : ''}</span>
                      </th>
                    )
                  })}
                </tr>
              ))}
            </thead>
            <tbody ref={bodyRef}>
              {table.getRowModel().rows.map((row) => {
                const a = row.original
                const newAge = now === 0 ? undefined : newAgeMs(a, now)
                const cls = [
                  newAge === undefined ? '' : 'new',
                  a.hasPosition ? '' : 'nopos',
                  isEmergency(a) ? 'emergency' : '',
                  a.icao === selected ? 'selected' : '',
                ]
                  .filter(Boolean)
                  .join(' ')
                return (
                  <tr
                    key={row.id}
                    data-icao={a.icao}
                    className={cls}
                    // the fade starts at the row's real age, so a page load mid-way doesn't restart it
                    style={newAge === undefined ? undefined : { animationDelay: `-${newAge}ms` }}
                    aria-selected={a.icao === selected}
                    onClick={() => {
                      dispatch({ type: 'select', icao: a.icao, pan: a.hasPosition })
                    }}
                  >
                    {row.getAllCells().map((cell) => (
                      <td key={cell.id} className={TEXT_COLUMNS.has(cell.column.id) ? '' : 'num'}>
                        <table.FlexRender cell={cell} />
                      </td>
                    ))}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </NowContext>
      </div>
    </section>
  )
}
