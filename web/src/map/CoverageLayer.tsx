import { useMemo, useState } from 'react'
import { Polygon, Tooltip } from 'react-leaflet'
import type { Coverage } from '../api/types'
import { coverageAt, coverageLabel, coveragePositions } from './coverage'

/**
 * CoverageLayer draws the range outline (furthest position per bearing bucket) as a lightly filled polygon
 * under the aircraft. Hovering shows the maximum range in the bearing under the cursor.
 */
export function CoverageLayer({ coverage }: { coverage: Coverage | null }) {
  const positions = useMemo(() => coveragePositions(coverage), [coverage])
  const [label, setLabel] = useState('')
  if (coverage === null || positions.length === 0) {
    return null
  }
  return (
    <Polygon
      positions={positions}
      pathOptions={{ className: 'coverage', weight: 1.5 }}
      eventHandlers={{
        mousemove: (e) => {
          const p = coverageAt(coverage, e.latlng.lat, e.latlng.lng)
          setLabel(p === undefined ? '' : coverageLabel(p))
        },
      }}
    >
      {label !== '' && (
        <Tooltip sticky direction="top" offset={[0, -8]}>
          {label}
        </Tooltip>
      )}
    </Polygon>
  )
}
