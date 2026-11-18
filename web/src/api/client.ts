import type { operations } from './schema'

export type ListTargets =
  operations['listTargets']['responses'][200]['content']['application/vnd.api+json']
export type ListScanPlans =
  operations['listScanPlans']['responses'][200]['content']['application/vnd.api+json']
export type ListRegions =
  operations['listRegions']['responses'][200]['content']['application/vnd.api+json']
export type ListInstanceTypes =
  operations['listInstanceTypes']['responses'][200]['content']['application/vnd.api+json']
export type ListRuns =
  operations['listRuns']['responses'][200]['content']['application/vnd.api+json']
export type ListResults =
  operations['listScanResults']['responses'][200]['content']['application/vnd.api+json']
export type ListRunWorkers =
  operations['listRunWorkers']['responses'][200]['content']['application/vnd.api+json']
export type Target = ListTargets['data'][number]
export type CreateTargetAttributes =
  operations['createTarget']['requestBody']['content']['application/vnd.api+json']['data']['attributes']
export type UpdateTargetAttributes =
  operations['updateTarget']['requestBody']['content']['application/vnd.api+json']['data']['attributes']
export type ScanPlan = ListScanPlans['data'][number]
export type CreateScanPlanAttributes =
  operations['createScanPlan']['requestBody']['content']['application/vnd.api+json']['data']['attributes']
export type UpdateScanPlanAttributes =
  operations['updateScanPlan']['requestBody']['content']['application/vnd.api+json']['data']['attributes']
export type ScanPlanSettings = ScanPlan['attributes']['settings']
export type Provider = 'vultr' | 'digitalocean'
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

export type ExportFormat = 'csv' | 'jsonl'

function errorDetail(body: unknown, status: number): string {
  const errors =
    body && typeof body === 'object' && 'errors' in body ? (body as { errors: unknown }).errors : []
  const detail = Array.isArray(errors)
    ? errors
        .map((error) => (typeof error?.detail === 'string' ? error.detail : ''))
        .filter(Boolean)
        .join('; ')
    : ''
  return detail || `Request failed (${status})`
}

async function apiError(response: Response): Promise<ApiError> {
  if (response.headers.get('content-type')?.split(';')[0] !== 'application/vnd.api+json')
    return new ApiError(response.status, `Request failed (${response.status})`)
  try {
    const body: unknown = await response.json()
    return new ApiError(response.status, errorDetail(body, response.status))
  } catch {
    return new ApiError(response.status, `Request failed (${response.status})`)
  }
}

function attachmentFilename(header: string | null): string {
  const match = header?.match(/(?:^|;)\s*filename="([^"\\/]+)"(?:;|$)/i)
  const name = match?.[1]
  if (!name || name === '.' || name === '..') throw new ApiError(502, 'Invalid export filename')
  return name
}

export async function downloadRunExport(id: string, format: ExportFormat): Promise<void> {
  const path = `/v1/runs/${encodeURIComponent(id)}/exports/${format}`
  const response = await fetch(path, {
    headers: { Accept: 'text/csv, application/x-ndjson, application/vnd.api+json' },
    credentials: 'same-origin',
  })
  if (!response.ok) throw await apiError(response)
  const contentType = response.headers.get('content-type')?.split(';')[0]
  if (contentType !== (format === 'csv' ? 'text/csv' : 'application/x-ndjson'))
    throw new ApiError(502, 'Unexpected export response type')
  const filename = attachmentFilename(response.headers.get('content-disposition'))
  const blob = await response.blob()
  const objectURL = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectURL
  link.download = filename
  document.body.append(link)
  try {
    link.click()
  } finally {
    link.remove()
    // Browsers may begin reading the object URL after the click handler returns.
    window.setTimeout(() => URL.revokeObjectURL(objectURL), 60_000)
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
  mutation?: { method: 'POST' | 'PATCH'; body: unknown },
): Promise<T> {
  if (!path.startsWith('/v1/')) throw new ApiError(400, 'Invalid API path')
  const headers: Record<string, string> = { Accept: 'application/vnd.api+json' }
  if (mutation) headers['Content-Type'] = 'application/vnd.api+json'
  const response = await fetch(path, {
    method: mutation?.method,
    body: mutation ? JSON.stringify(mutation.body) : undefined,
    headers,
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
    throw new ApiError(response.status, errorDetail(body, response.status))
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
  targetByName: (name: string, signal?: AbortSignal) =>
    request<{ data: Target }>(
      `/v1/targets/by-name/${encodeURIComponent(name)}`,
      'targets',
      false,
      signal,
    ),
  createTarget: (attributes: CreateTargetAttributes) =>
    request<{ data: Target }>('/v1/targets', 'targets', false, undefined, {
      method: 'POST',
      body: { data: { type: 'targets', attributes } },
    }),
  updateTarget: (id: string, attributes: UpdateTargetAttributes) =>
    request<{ data: Target }>(
      `/v1/targets/${encodeURIComponent(id)}`,
      'targets',
      false,
      undefined,
      {
        method: 'PATCH',
        body: { data: { type: 'targets', id, attributes } },
      },
    ),
  archiveTarget: (id: string, revision: number) =>
    api.updateTarget(id, { revision, archived: true }),
  plans: (query?: Record<string, string | number | undefined>, signal?: AbortSignal) =>
    request<ListScanPlans>(url('/v1/scan-plans', query), 'scan-plans', true, signal),
  plan: (id: string, signal?: AbortSignal) =>
    request<{ data: ScanPlan }>(
      `/v1/scan-plans/${encodeURIComponent(id)}`,
      'scan-plans',
      false,
      signal,
    ),
  createPlan: (attributes: CreateScanPlanAttributes) =>
    request<{ data: ScanPlan }>('/v1/scan-plans', 'scan-plans', false, undefined, {
      method: 'POST',
      body: { data: { type: 'scan-plans', attributes } },
    }),
  updatePlan: (id: string, attributes: UpdateScanPlanAttributes) =>
    request<{ data: ScanPlan }>(
      `/v1/scan-plans/${encodeURIComponent(id)}`,
      'scan-plans',
      false,
      undefined,
      { method: 'PATCH', body: { data: { type: 'scan-plans', id, attributes } } },
    ),
  archivePlan: (id: string, revision: number) => api.updatePlan(id, { revision, archived: true }),
  regions: (provider: Provider, signal?: AbortSignal) =>
    request<ListRegions>(url('/v1/catalog/regions', { provider }), 'regions', true, signal),
  instanceTypes: (provider: Provider, region: string, signal?: AbortSignal) =>
    request<ListInstanceTypes>(
      url('/v1/catalog/instance-types', { provider, region }),
      'instance-types',
      true,
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
