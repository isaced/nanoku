import {
  MutationCache,
  QueryCache,
  QueryClient,
  type QueryClientConfig,
} from '@tanstack/react-query'
import { ApiError, triggerUnauthorized } from './api'

export function createAppQueryClient(
  overrides: QueryClientConfig = {},
): QueryClient {
  const onError = (err: unknown) => {
    if (err instanceof ApiError && err.status === 401) {
      triggerUnauthorized()
    }
  }
  return new QueryClient({
    ...overrides,
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        // Disable focus-driven refetch. Each page already self-polls
        // (Dashboard 5s, Apps detail drawer 3s for deploys, System
        // 30s for cleanup); letting TanStack Query also refetch on
        // every focus event multiplies the request count and races
        // with the in-page polling. A user returning to a tab sees
        // fresh-enough data via the in-page poll + the 30s staleTime
        // window; anything they actively want refreshed has a manual
        // refresh button.
        refetchOnWindowFocus: false,
        retry: 1,
        ...overrides.defaultOptions?.queries,
      },
      ...overrides.defaultOptions,
    },
    queryCache: new QueryCache({
      ...overrides.queryCache,
      onError,
    }),
    mutationCache: new MutationCache({
      ...overrides.mutationCache,
      onError,
    }),
  })
}

