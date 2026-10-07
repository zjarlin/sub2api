export interface SearchableMenuItem {
  path: string
  label: string
  searchKeywords?: string
  children?: SearchableMenuItem[]
}

export function filterMenuItems<T extends SearchableMenuItem>(items: T[], query: string): T[] {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  if (terms.length === 0) {
    return items
  }

  return items.flatMap(item => {
    const text = `${item.label} ${item.path} ${item.searchKeywords || ''}`.toLocaleLowerCase()
    if (terms.every(term => text.includes(term))) {
      return [item]
    }
    if (item.children) {
      const children = filterMenuItems(item.children, query)
      if (children.length > 0) {
        return [{ ...item, children }]
      }
    }
    return []
  })
}
