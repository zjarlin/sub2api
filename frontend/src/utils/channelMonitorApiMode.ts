import type { Provider } from '@/api/admin/channelMonitor'
import {
  PROVIDER_GEMINI,
  PROVIDER_OPENAI,
} from '@/constants/channelMonitor'

export function supportsResponsesApiMode(provider: Provider | string | undefined | null): boolean {
  return provider === PROVIDER_OPENAI || provider === PROVIDER_GEMINI
}
