import type { Notable } from '../api/stats'
import { linkClick } from '../route'
import { momentSearch } from './aggregate'

const timeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

/** NotableList lists emergencies and first-seen addresses; each row opens the map in playback with the aircraft. */
export function NotableList({ notable }: { notable: Notable }) {
  const { events, emergencies, firstSeen } = notable
  const more = emergencies + firstSeen - events.length
  return (
    <>
      <p className="stats-hint">
        {emergencies} emergenc{emergencies === 1 ? 'y' : 'ies'} · {firstSeen} address{firstSeen === 1 ? '' : 'es'} seen
        for the first time. Click a row to replay that moment.
      </p>
      <ul className="notable-list">
        {events.map((e) => {
          const search = momentSearch(e.t, e.icao)
          return (
            <li key={`${e.kind}/${e.icao}/${String(e.flightId)}/${e.squawk ?? ''}`} className={e.kind}>
              <a href={`/${search}`} onClick={linkClick('/', search)}>
                <span className="notable-kind">{e.kind === 'emergency' ? e.squawk : 'NEW'}</span>
                <time dateTime={e.t}>{timeFmt.format(new Date(e.t))}</time>
                <span className="notable-icao">{e.icao}</span>
                <span>{e.callsign ?? ''}</span>
                <span className="muted">{e.country ?? ''}</span>
              </a>
            </li>
          )
        })}
      </ul>
      {more > 0 && <p className="stats-hint">and {more} more; pick a shorter range to see them.</p>}
    </>
  )
}
