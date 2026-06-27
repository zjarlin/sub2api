export interface ModelsListConfig {
  enabled: boolean
  models: string[]
  model_rate_multipliers?: Record<string, number>
}

export interface ModelsListItem {
  id: string
  selected: boolean
  rateMultiplier: number | null
}

export interface ModelsListState {
  enabled: boolean
  savedModels: string[]
  savedModelRateMultipliers: Record<string, number>
  items: ModelsListItem[]
}

export const createModelsListState = (
  config?: Partial<ModelsListConfig> | null,
): ModelsListState => ({
  enabled: config?.enabled ?? false,
  savedModels: normalizeModels(config?.models ?? []),
  savedModelRateMultipliers: normalizeModelRateMultipliers(config?.model_rate_multipliers ?? {}),
  items: [],
})

export const hydrateModelsListState = (
  config: Partial<ModelsListConfig> | null | undefined,
  candidates: string[],
): ModelsListState => {
  const state = createModelsListState(config)
  setModelsListCandidates(state, candidates)
  return state
}

export const setModelsListCandidates = (
  state: ModelsListState,
  candidates: string[],
) => {
  const normalizedCandidates = normalizeModels(candidates)
  const currentSelected = new Set(
    state.items.filter(item => item.selected).map(item => item.id),
  )
  const currentKnown = new Set(state.items.map(item => item.id))
  const savedSelected = new Set(state.savedModels)
  const currentRates = new Map(
    state.items.map(item => [item.id, item.rateMultiplier] as const),
  )
  const hasExistingItems = state.items.length > 0
  const selectionOrder = normalizeModels([
    ...state.items.map(item => item.id),
    ...state.savedModels,
    ...normalizedCandidates,
  ])

  state.items = selectionOrder.map(id => {
    const selected = hasExistingItems
      ? currentSelected.has(id)
      : state.savedModels.length > 0
        ? savedSelected.has(id)
        : normalizedCandidates.includes(id)

    return {
      id,
      selected: selected && (currentKnown.has(id) || savedSelected.has(id) || state.savedModels.length === 0),
      rateMultiplier: currentRates.has(id)
        ? currentRates.get(id) ?? null
        : state.savedModelRateMultipliers[id] ?? null,
    }
  })
}

export const toggleModelsListItem = (state: ModelsListState, modelID: string) => {
  const item = state.items.find(item => item.id === modelID)
  if (item) {
    item.selected = !item.selected
  }
}

export const selectAllModelsListItems = (state: ModelsListState) => {
  state.items.forEach(item => {
    item.selected = true
  })
}

export const invertModelsListSelection = (state: ModelsListState) => {
  state.items.forEach(item => {
    item.selected = !item.selected
  })
}

export const moveModelsListItem = (
  state: ModelsListState,
  fromIndex: number,
  toIndex: number,
) => {
  if (
    fromIndex === toIndex ||
    fromIndex < 0 ||
    toIndex < 0 ||
    fromIndex >= state.items.length ||
    toIndex >= state.items.length
  ) {
    return
  }
  const [item] = state.items.splice(fromIndex, 1)
  state.items.splice(toIndex, 0, item)
}

export const buildModelsListConfig = (state: ModelsListState): ModelsListConfig => {
  const config: ModelsListConfig = {
    enabled: state.enabled,
    models: state.items.length > 0
      ? state.items.filter(item => item.selected).map(item => item.id)
      : [...state.savedModels],
  }
  const rates = buildSelectedModelRateMultipliers(state)
  if (rates) {
    config.model_rate_multipliers = rates
  }
  return config
}

export const updateModelsListItemRate = (
  state: ModelsListState,
  modelID: string,
  rawValue: number | string | null,
) => {
  const item = state.items.find(item => item.id === modelID)
  if (!item) {
    return
  }
  item.rateMultiplier = normalizeModelRate(rawValue)
}

const normalizeModels = (models: string[]): string[] => {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of models) {
    const model = raw.trim()
    if (!model || seen.has(model)) {
      continue
    }
    seen.add(model)
    out.push(model)
  }
  return out
}

const normalizeModelRate = (value: number | string | null | undefined): number | null => {
  if (value === null || value === undefined || value === '') {
    return null
  }
  const rate = Number(value)
  return Number.isFinite(rate) && rate > 0 ? rate : null
}

const normalizeModelRateMultipliers = (
  rates: Record<string, number | string | null | undefined>,
): Record<string, number> => {
  const out: Record<string, number> = {}
  for (const [rawModel, rawRate] of Object.entries(rates)) {
    const model = rawModel.trim()
    const rate = normalizeModelRate(rawRate)
    if (!model || rate == null) {
      continue
    }
    out[model] = rate
  }
  return out
}

const buildSelectedModelRateMultipliers = (state: ModelsListState): Record<string, number> | undefined => {
  const selectedModels = state.items.length > 0
    ? state.items.filter(item => item.selected)
    : state.savedModels.map(id => ({
      id,
      selected: true,
      rateMultiplier: state.savedModelRateMultipliers[id] ?? null,
    }))

  const out: Record<string, number> = {}
  for (const item of selectedModels) {
    if (item.rateMultiplier != null && item.rateMultiplier > 0) {
      out[item.id] = item.rateMultiplier
    }
  }
  return Object.keys(out).length > 0 ? out : undefined
}
