export type SourceCountVariant = 'live' | 'delayed' | 'accent'

export interface SourceCountFact {
  available: boolean
  source: string
  contributor_count: number
  contributors: string[]
}

export interface SourceCountBadge {
  count: number
  label: '3+ sources' | '2 sources' | '1 source' | 'unavailable'
  variant: SourceCountVariant
}

export function sourceCountBadge(fact: SourceCountFact): SourceCountBadge {
  if (!fact.available) return { count: 0, label: 'unavailable', variant: 'accent' }

  const declared = Number.isFinite(fact.contributor_count)
    ? Math.max(0, Math.floor(fact.contributor_count))
    : 0
  const named = new Set(fact.contributors.map((source) => source.trim()).filter(Boolean)).size
  const count = Math.max(declared, named, fact.source.trim() ? 1 : 0)

  if (count >= 3) return { count, label: '3+ sources', variant: 'live' }
  if (count === 2) return { count, label: '2 sources', variant: 'delayed' }
  if (count === 1) return { count, label: '1 source', variant: 'accent' }
  return { count: 0, label: 'unavailable', variant: 'accent' }
}
