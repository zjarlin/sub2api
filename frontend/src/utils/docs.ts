export const BUILT_IN_DOCS_PATH = '/docs'

export function resolveDocsUrl(configuredUrl?: string | null): string {
  const trimmed = configuredUrl?.trim() ?? ''
  return trimmed || BUILT_IN_DOCS_PATH
}
