// Aircraft icons by ADS-B emitter category (DO-260B: set A aircraft, B other flying things, C surface). All are
// drawn here, nose up on a 24 × 24 grid, filled with currentColor.

const JET =
  '<path d="M12 1.5c.8 0 1.3.7 1.3 1.6V9l8 4.8v2.1l-8-2.4v5l2.3 1.7v1.8L12 21.1 8.4 22v-1.8l2.3-1.7v-5l-8 2.4v-2.1l8-4.8V3.1c0-.9.5-1.6 1.3-1.6z"/>'

/** IconKind is the icon shape of an aircraft. */
export type IconKind =
  | 'generic'
  | 'light'
  | 'small'
  | 'large'
  | 'heavy'
  | 'fast'
  | 'rotor'
  | 'glider'
  | 'balloon'
  | 'drone'
  | 'vehicle'

interface IconSpec {
  label: string // for the legend
  shape: string // SVG elements on the 24 × 24 grid
  size: number // drawn size in px at scale 1
  rotates: boolean // turns with the track
}

export const ICONS: Record<IconKind, IconSpec> = {
  generic: { label: 'category unknown', shape: JET, size: 24, rotates: true },
  light: {
    label: 'light, ultralight',
    shape: '<path d="M10.4 3.4Q12 1.4 13.6 3.4V6.6h7.4v4.4h-7.4v5.8l3.4 1v2.8H7v-2.8l3.4-1V11H3V6.6h7.4z"/>',
    size: 22,
    rotates: true,
  },
  small: { label: 'small (7–34 t)', shape: JET, size: 20, rotates: true },
  large: { label: 'large (34–136 t)', shape: JET, size: 24, rotates: true },
  heavy: {
    label: 'heavy (> 136 t)',
    shape:
      '<path d="M12 1c1 0 1.6.9 1.6 2v5.5l3 1.8V9h1.4v2.1l4.5 2.7v2.2l-9-2.8v5.2l2.6 1.9V22L12 21.2 7.9 22v-1.7l2.6-1.9v-5.2l-9 2.8v-2.2l4.5-2.7V9h1.4v1.3l3-1.8V3c0-1.1.6-2 1.6-2z"/>',
    size: 30,
    rotates: true,
  },
  fast: {
    label: 'high performance',
    shape: '<path d="M12 1l1.5 5 .5 5 7 7v2l-7-2-.3 2 2.3 1.6V23L12 22l-4 1v-1.4l2.3-1.6-.3-2-7 2v-2l7-7 .5-5z"/>',
    size: 24,
    rotates: true,
  },
  rotor: {
    label: 'helicopter',
    shape:
      '<rect x="10.9" y="-3" width="2.2" height="26" rx="1.1" transform="rotate(45 12 10)"/><rect x="10.9" y="-3" width="2.2" height="26" rx="1.1" transform="rotate(-45 12 10)"/><ellipse cx="12" cy="10" rx="3.4" ry="4.6"/><path d="M11 13h2v6.6h3.4v2.4H7.6v-2.4H11z"/>',
    size: 24,
    rotates: true,
  },
  glider: {
    label: 'glider',
    shape: '<path d="M11 4Q12 2.6 13 4v5.3h10.6v1.9H13v7.6l2.6.5v1.9H8.4v-1.9l2.6-.5v-7.6H.4V9.3H11z"/>',
    size: 24,
    rotates: true,
  },
  balloon: {
    label: 'balloon, airship',
    shape: '<path d="M12 2a7 7 0 0 1 7 7c0 3.3-2.6 6-5 7.6V18h-4v-1.4C7.6 15 5 12.3 5 9a7 7 0 0 1 7-7zM10 19h4v3h-4z"/>',
    size: 22,
    rotates: false,
  },
  drone: {
    label: 'UAV',
    shape:
      '<path d="M5.4 3.6l15 15-1.8 1.8-15-15zM18.6 3.6l1.8 1.8-15 15-1.8-1.8z"/><rect x="9" y="9" width="6" height="6" rx="1.2"/><path fill-rule="evenodd" d="M5 1.5a3.5 3.5 0 1 1 0 7 3.5 3.5 0 0 1 0-7zm0 2a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM19 1.5a3.5 3.5 0 1 1 0 7 3.5 3.5 0 0 1 0-7zm0 2a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM5 15.5a3.5 3.5 0 1 1 0 7 3.5 3.5 0 0 1 0-7zm0 2a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM19 15.5a3.5 3.5 0 1 1 0 7 3.5 3.5 0 0 1 0-7zm0 2a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3z"/>',
    size: 20,
    rotates: true,
  },
  vehicle: {
    label: 'ground vehicle',
    shape: '<path d="M8 4h8l1 3v13a1 1 0 0 1-1 1H8a1 1 0 0 1-1-1V7z"/>',
    size: 16,
    rotates: true,
  },
}

/** Legend order of the kinds. */
export const ICON_ORDER: readonly IconKind[] = [
  'heavy', 'large', 'small', 'light', 'fast', 'rotor', 'glider', 'balloon', 'drone', 'vehicle', 'generic',
]

const BY_CATEGORY: Record<string, IconKind> = {
  A1: 'light', A2: 'small', A3: 'large', A4: 'large', A5: 'heavy', A6: 'fast', A7: 'rotor',
  B1: 'glider', B2: 'balloon', B4: 'light', B6: 'drone', C1: 'vehicle', C2: 'vehicle',
}

/** iconFor returns the icon of an emitter category ("A3"); unknown, absent or rare categories get the generic one. */
export function iconFor(category: string | undefined): IconKind {
  return (category === undefined ? undefined : BY_CATEGORY[category]) ?? 'generic'
}

/**
 * iconSvg returns the icon's SVG markup at scale (1: its size in px on the map): the shape over an outline of the
 * whole shape (class ac-halo), so the outline never cuts into overlapping parts.
 */
export function iconSvg(kind: IconKind, scale = 1): string {
  const px = Math.round(ICONS[kind].size * scale)
  const { shape } = ICONS[kind]
  return `<svg viewBox="0 0 24 24" width="${String(px)}" height="${String(px)}" aria-hidden="true"><g class="ac-halo">${shape}</g><g>${shape}</g></svg>`
}

/** iconRotation is the rotation of the icon: the aircraft's track for kinds that turn, else 0. */
export function iconRotation(kind: IconKind, deg: number): number {
  return ICONS[kind].rotates ? deg : 0
}

const CATEGORY_NAMES: Record<string, string> = {
  A1: 'light', A2: 'small', A3: 'large', A4: 'high-vortex large', A5: 'heavy', A6: 'high performance',
  A7: 'rotorcraft', B1: 'glider', B2: 'lighter than air', B3: 'parachutist', B4: 'ultralight', B6: 'UAV',
  B7: 'space vehicle', C1: 'emergency vehicle', C2: 'service vehicle', C3: 'point obstacle', C4: 'cluster obstacle',
  C5: 'line obstacle',
}

/** categoryText is the category code with its meaning, e.g. "A3 (large)"; undefined stays undefined. */
export function categoryText(category: string | undefined): string | undefined {
  if (category === undefined) {
    return undefined
  }
  const name = CATEGORY_NAMES[category]
  return name === undefined ? category : `${category} (${name})`
}
