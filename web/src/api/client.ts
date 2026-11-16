import type { operations } from './schema'

export type ListTargets =
  operations['listTargets']['responses'][200]['content']['application/vnd.api+json']
export type ListScanPlans =
  operations['listScanPlans']['responses'][200]['content']['application/vnd.api+json']
export type ListRuns =
  operations['listRuns']['responses'][200]['content']['application/vnd.api+json']
export type ListResults =
  operations['listScanResults']['responses'][200]['content']['application/vnd.api+json']
export type ListRunWorkers =
  operations['listRunWorkers']['responses'][200]['content']['application/vnd.api+json']
export type Target = ListTargets['data'][number]
export type ScanPlan = ListScanPlans['data'][number]
export type ScanRun = ListRuns['data'][number]
export type ScanResult = ListResults['data'][number]
export type RunWorker = ListRunWorkers['data'][number]
export type Collection<T> = {
  data: T[]
  links?: Record<string, unknown>
  meta?: Record<string, unknown>
}
export type IncludedResource = {
  type: string
  id: string
  attributes: Record<string, unknown>
  relationships?: Record<string, unknown>
}

export function relatedResources(
  document: { data: unknown; included?: IncludedResource[] },
  resource: { relationships?: Record<string, unknown> },
  name: string,
): IncludedResource[] {
  const relation = resource.relationships?.[name]
  if (!relation || typeof relation !== 'object' || !('data' in relation)) return []
  const identifiers = Array.isArray(relation.data) ? relation.data : [relation.data]
  return identifiers.flatMap((identifier) => {
    if (!identifier || typeof identifier !== 'object') return []
    const found = document.included?.find(
      (item) => item.type === identifier.type && item.id === identifier.id,
    )
    return found ? [found] : []
  })
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

function expectDocument<T>(value: unknown, type: string, collection: boolean): T {
  if (!value || typeof value !== 'object' || !('data' in value))
    throw new ApiError(502, 'Invalid API document')
  const document = value as { data: unknown }
  const resources = collection ? document.data : [document.data]
  if (
    !Array.isArray(resources) ||
    resources.some(
      (item) =>
        !item ||
        typeof item !== 'object' ||
        (item as Record<string, unknown>).type !== type ||
        typeof (item as Record<string, unknown>).id !== 'string' ||
        !(item as Record<string, unknown>).attributes,
    )
  ) {
    throw new ApiError(502, 'Invalid API resource')
  }
  if ('included' in document) {
    if (
      !Array.isArray(document.included) ||
      document.included.some(
        (item) =>
          !item ||
          typeof item !== 'object' ||
          typeof item.type !== 'string' ||
          typeof item.id !== 'string' ||
          !item.attributes ||
          typeof item.attributes !== 'object',
      )
    )
      throw new ApiError(502, 'Invalid included resource')
  }
  return value as T
}

async function request<T>(
  path: string,
  type: string,
  collection: boolean,
  signal?: AbortSignal,
): Promise<T> {
  if (!path.startsWith('/v1/')) throw new ApiError(400, 'Invalid API path')
  const response = await fetch(path, {
    headers: { Accept: 'application/vnd.api+json' },
    credentials: 'same-origin',
    signal,
  })
  const contentType = response.headers.get('content-type')?.split(';')[0]
  if (contentType !== 'application/vnd.api+json')
    throw new ApiError(response.status, 'Unexpected API response type')
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new ApiError(response.status, 'Invalid API JSON')
  }
  if (!response.ok) {
    const errors =
      body && typeof body === 'object' && 'errors' in body
        ? (body as { errors: unknown }).errors
        : []
    const detail = Array.isArray(errors)
      ? errors
          .map((error) => (typeof error?.detail === 'string' ? error.detail : ''))
          .filter(Boolean)
          .join('; ')
      : ''
    throw new ApiError(response.status, detail || `Request failed (${response.status})`)
  }
  return expectDocument<T>(body, type, collection)
}

function url(path: string, query: Record<string, string | number | undefined> = {}) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query))
    if (value !== undefined && value !== '') params.set(key, String(value))
  return path + (params.size ? `?${params}` : '')
}

export const api = {
  ping: async (signal?: AbortSignal) => {
    const response = await fetch('/healthz', { credentials: 'same-origin', signal })
    if (!response.ok) throw new ApiError(response.status, 'Server unavailable')
    const body: unknown = await response.json()
    if (
      !body ||
      typeof body !== 'object' ||
      !('status' in body) ||
      (body as { status: unknown }).status !== 'ready'
    )
      throw new ApiError(502, 'Invalid health response')
    return 'Ready' as const
  },
  targets: (query?: Record<string, string | number | undefined>, signal?: AbortSignal) =>
    request<ListTargets>(url('/v1/targets', query), 'targets', true, signal),
  target: (id: string, signal?: AbortSignal) =>
    request<{ data: Target }>(`/v1/targets/${encodeURIComponent(id)}`, 'targets', false, signal),
  plans: (query?: Record<string, string | number | undefined>, signal?: AbortSignal) =>
    request<ListScanPlans>(url('/v1/scan-plans', query), 'scan-plans', true, signal),
  plan: (id: string, signal?: AbortSignal) =>
    request<{ data: ScanPlan }>(
      `/v1/scan-plans/${encodeURIComponent(id)}`,
      'scan-plans',
      false,
      signal,
    ),
  runs: (query?: Record<string, string | number | undefined>, signal?: AbortSignal) =>
    request<ListRuns>(url('/v1/runs', query), 'runs', true, signal),
  run: (id: string, signal?: AbortSignal) =>
    request<{ data: ScanRun }>(`/v1/runs/${encodeURIComponent(id)}`, 'runs', false, signal),
  runWorkers: (
    id: string,
    query?: Record<string, string | number | undefined>,
    signal?: AbortSignal,
  ) =>
    request<ListRunWorkers>(
      url(`/v1/runs/${encodeURIComponent(id)}/workers`, query),
      'run-workers',
      true,
      signal,
    ),
  results: (query?: Record<string, string | number | undefined>, signal?: AbortSignal) =>
    request<ListResults>(url('/v1/scan-results', query), 'scan-results', true, signal),
}

export function nextCursor(link: unknown): string | undefined {
  if (typeof link !== 'string') return undefined
  try {
    return new URL(link, window.location.origin).searchParams.get('after') ?? undefined
  } catch {
    return undefined
  }
}
