import type { Aircraft } from '../api/types'
import { iconFor, iconRotation, iconSvg, ICONS } from './icons'
import { type ColorMode, markerColor, rotation } from './style'

const TABLE_SCALE = 0.75

/** AircraftIcon is the aircraft's map icon, in its marker color and rotation, for the table. */
export function AircraftIcon({ a, mode }: { a: Aircraft; mode: ColorMode }) {
  const kind = iconFor(a.category)
  return (
    <span
      className="ac-icon"
      title={ICONS[kind].label}
      style={{ color: markerColor(a, mode), transform: `rotate(${String(iconRotation(kind, rotation(a)))}deg)` }}
      dangerouslySetInnerHTML={{ __html: iconSvg(kind, TABLE_SCALE) }}
    />
  )
}
