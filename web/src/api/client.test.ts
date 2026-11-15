import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, nextCursor, relatedResources } from './client'

afterEach(() => vi.unstubAllGlobals())
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
})
