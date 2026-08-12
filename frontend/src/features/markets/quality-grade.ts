export type QualityGradeVariant = 'live' | 'delayed' | 'accent'

export interface QualityGradeFact {
  available: boolean
  source: string
  quality: string
  contributor_count: number
  contributors: string[]
}

export interface QualityGradeBadge {
  count: number
  label: 'High' | 'Medium' | 'Low' | 'Unavailable'
  variant: QualityGradeVariant
}

function contributorCount(fact: QualityGradeFact): number {
  const cexProviders = new Set(['binance', 'coinbase', 'bybit', 'okx'])
  const identities = [...new Set(
    fact.contributors.map((source) => source.trim().toLowerCase()).filter(Boolean),
  )]
  if (identities.length > 0) {
    return identities.filter((source) => cexProviders.has(source)).length
  }
  const source = fact.source.trim().toLowerCase()
  return cexProviders.has(source) ? 1 : 0
}

export function qualityGradeBadge(fact: QualityGradeFact): QualityGradeBadge {
  if (!fact.available) return { count: 0, label: 'Unavailable', variant: 'accent' }

  const count = contributorCount(fact)
  if (count === 0) return { count: 0, label: 'Unavailable', variant: 'accent' }
  if (count >= 3) return { count, label: 'High', variant: 'live' }
  if (count === 2) return { count, label: 'Medium', variant: 'delayed' }
  return { count, label: 'Low', variant: 'accent' }
}
