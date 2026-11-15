import { queryOptions } from '@tanstack/react-query'
import { api } from './client'

export const queries = {
  targets: (limit = 100, after?: string) =>
    queryOptions({
      queryKey: ['targets', limit, after],
      queryFn: ({ signal }) => api.targets({ limit, after }, signal),
    }),
  plans: (limit = 100, after?: string) =>
    queryOptions({
      queryKey: ['plans', limit, after],
      queryFn: ({ signal }) => api.plans({ limit, after }, signal),
    }),
  runs: (limit = 100, after?: string) =>
    queryOptions({
      queryKey: ['runs', limit, after],
      queryFn: ({ signal }) => api.runs({ limit, after }, signal),
      refetchInterval: (query) =>
        query.state.data?.data.some((run) => run.attributes.active) ? 5000 : false,
    }),
  activeRuns: () =>
    queryOptions({
      queryKey: ['runs', 'active'],
      queryFn: ({ signal }) => api.runs({ active: 'true' }, signal),
      refetchInterval: 5000,
    }),
  results: (params: Record<string, string | number | undefined>) =>
    queryOptions({
      queryKey: ['results', params],
      queryFn: ({ signal }) => api.results(params, signal),
    }),
}
