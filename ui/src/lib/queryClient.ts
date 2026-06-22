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
        refetchOnWindowFocus: true,
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

