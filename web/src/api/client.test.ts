import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, downloadRunExport, nextCursor, relatedResources } from './client'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
function reply(body: unknown, status = 200, contentType = 'application/vnd.api+json') {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': contentType } })
}

describe('JSON:API client', () => {
  it('requests same-origin typed resources and preserves pagination links', async () => {
    const fetch = vi.fn().mockResolvedValue(
      reply({
        data: [{ type: 'targets', id: 't-1', attributes: { name: 'External', entries: [] } }],
        links: { next: '/v1/targets?after=t-1' },
      }),
    )
    vi.stubGlobal('fetch', fetch)
    const result = await api.targets({ limit: 1 })
    expect(fetch).toHaveBeenCalledWith(
      '/v1/targets?limit=1',
      expect.objectContaining({
        credentials: 'same-origin',
        headers: { Accept: 'application/vnd.api+json' },
      }),
    )
    expect(result.data[0].attributes.name).toBe('External')
    expect(nextCursor(result.links?.next)).toBe('t-1')
  })
  it('surfaces JSON:API errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(reply({ errors: [{ status: '404', detail: 'not found' }] }, 404)),
    )
    await expect(api.target('missing')).rejects.toEqual(new ApiError(404, 'not found'))
  })
  it('rejects invalid resource types and response media types', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(reply({ data: [{ type: 'runs', id: 'wrong', attributes: {} }] }))
        .mockResolvedValueOnce(reply({}, 200, 'text/html')),
    )
    await expect(api.targets()).rejects.toThrow('Invalid API resource')
    await expect(api.targets()).rejects.toThrow('Unexpected API response type')
  })
  it('resolves JSON:API included relationships without extra requests', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        reply({
          data: [
            {
              type: 'scan-plans',
              id: 'p-1',
              attributes: { name: 'Routine' },
              relationships: { target: { data: { type: 'targets', id: 't-1' } } },
            },
          ],
          included: [{ type: 'targets', id: 't-1', attributes: { name: 'External' } }],
        }),
      ),
    )
    const document = await api.plans()
    expect(relatedResources(document, document.data[0], 'target')[0].attributes.name).toBe(
      'External',
    )
  })
  it('serializes Target create, revisioned update, and archive envelopes', async () => {
    const fetch = vi.fn().mockImplementation(() =>
      Promise.resolve(
        reply({
          data: { type: 'targets', id: 't-1', attributes: { name: 'External', entries: [] } },
        }),
      ),
    )
    vi.stubGlobal('fetch', fetch)
    await api.createTarget({ name: 'External', entries: ['192.0.2.1', 'AS3333'] })
    await api.updateTarget('t-1', { revision: 2, entries: ['192.0.2.0/24'] })
    await api.archiveTarget('t-1', 3)
    expect(
      fetch.mock.calls.map(([path, options]) => [path, options.method, JSON.parse(options.body)]),
    ).toEqual([
      [
        '/v1/targets',
        'POST',
        {
          data: {
            type: 'targets',
            attributes: { name: 'External', entries: ['192.0.2.1', 'AS3333'] },
          },
        },
      ],
      [
        '/v1/targets/t-1',
        'PATCH',
        {
          data: {
            type: 'targets',
            id: 't-1',
            attributes: { revision: 2, entries: ['192.0.2.0/24'] },
          },
        },
      ],
      [
        '/v1/targets/t-1',
        'PATCH',
        { data: { type: 'targets', id: 't-1', attributes: { revision: 3, archived: true } } },
      ],
    ])
    for (const [, options] of fetch.mock.calls) {
      expect(options.credentials).toBe('same-origin')
      expect(options.headers).toEqual({
        Accept: 'application/vnd.api+json',
        'Content-Type': 'application/vnd.api+json',
      })
    }
  })
  it('encodes Target names in lookup URLs', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        reply({ data: { type: 'targets', id: 't-1', attributes: { name: 'Cloud/Edge 100%' } } }),
      )
    vi.stubGlobal('fetch', fetch)
    await api.targetByName('Cloud/Edge 100%')
    expect(fetch.mock.calls[0][0]).toBe('/v1/targets/by-name/Cloud%2FEdge%20100%25')
  })
  it('preserves revision-conflict status and JSON:API detail', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(reply({ errors: [{ status: '409', detail: 'conflict' }] }, 409)),
    )
    await expect(api.updateTarget('t-1', { revision: 1, entries: ['192.0.2.1'] })).rejects.toEqual(
      new ApiError(409, 'conflict'),
    )
  })
  it('serializes ScanPlan create, partial edit, and archive with the shared client', async () => {
    const fetch = vi
      .fn()
      .mockImplementation(() =>
        Promise.resolve(
          reply({ data: { type: 'scan-plans', id: 'p-1', attributes: { name: 'Routine' } } }),
        ),
      )
    vi.stubGlobal('fetch', fetch)
    const settings = {
      backend: 'masscan',
      ports: '22,80,443',
      provider: 'vultr',
      region: 'atl',
      instance_type: 'vc2-1c-1gb',
      workers: 1,
      rate: 1000,
      timeout: '24h',
      cleanup_timeout: '5m',
    }
    await api.createPlan({ name: 'Routine', target_name: 'External', settings })
    await api.updatePlan('p-1', { revision: 2, settings: { ports: '443' } })
    await api.archivePlan('p-1', 3)
    expect(
      fetch.mock.calls.map(([path, options]) => [path, options.method, JSON.parse(options.body)]),
    ).toEqual([
      [
        '/v1/scan-plans',
        'POST',
        {
          data: {
            type: 'scan-plans',
            attributes: { name: 'Routine', target_name: 'External', settings },
          },
        },
      ],
      [
        '/v1/scan-plans/p-1',
        'PATCH',
        {
          data: {
            type: 'scan-plans',
            id: 'p-1',
            attributes: { revision: 2, settings: { ports: '443' } },
          },
        },
      ],
      [
        '/v1/scan-plans/p-1',
        'PATCH',
        { data: { type: 'scan-plans', id: 'p-1', attributes: { revision: 3, archived: true } } },
      ],
    ])
    expect(fetch.mock.calls.every(([, options]) => options.credentials === 'same-origin')).toBe(
      true,
    )
  })
  it('reads provider-aware region and instance catalogs', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(reply({ data: [] })))
    vi.stubGlobal('fetch', fetch)
    await api.regions('digitalocean')
    await api.instanceTypes('digitalocean', 'tor1')
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      '/v1/catalog/regions?provider=digitalocean',
      '/v1/catalog/instance-types?provider=digitalocean&region=tor1',
    ])
  })
})

describe('run exports', () => {
  it.each([
    ['csv', 'text/csv', 'run-1.csv'],
    ['jsonl', 'application/x-ndjson', 'run-1.jsonl'],
  ] as const)(
    'downloads %s using the server attachment name',
    async (format, mediaType, filename) => {
      const fetch = vi.fn().mockResolvedValue(
        new Response('saved results', {
          headers: {
            'Content-Type': mediaType,
            'Content-Disposition': `attachment; filename="${filename}"`,
          },
        }),
      )
      const createObjectURL = vi.fn().mockReturnValue('blob:export')
      vi.stubGlobal('fetch', fetch)
      vi.stubGlobal(
        'URL',
        class extends URL {
          static createObjectURL = createObjectURL
          static revokeObjectURL = vi.fn()
        },
      )
      let clickedName = ''
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
        this: HTMLAnchorElement,
      ) {
        clickedName = this.download
      })
      await downloadRunExport('run-1', format)
      expect(fetch).toHaveBeenCalledWith(`/v1/runs/run-1/exports/${format}`, {
        headers: { Accept: 'text/csv, application/x-ndjson, application/vnd.api+json' },
        credentials: 'same-origin',
      })
      expect(clickedName).toBe(filename)
      expect(createObjectURL).toHaveBeenCalledWith(expect.any(Blob))
    },
  )

  it('surfaces an unavailable saved file without offering a partial download', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          reply({ errors: [{ status: '409', detail: 'saved export missing' }] }, 409),
        ),
    )
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click')
    await expect(downloadRunExport('run-1', 'csv')).rejects.toEqual(
      new ApiError(409, 'saved export missing'),
    )
    expect(click).not.toHaveBeenCalled()
  })
})
