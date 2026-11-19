import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, cleanup, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { TargetsPage } from './targets'
import { ResultsPage } from './results'
import { RunDetailPage } from './runs'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})
function respond(body: unknown) {
  return new Response(JSON.stringify(body), {
    headers: { 'Content-Type': 'application/vnd.api+json' },
  })
}
function renderPage(component: React.ReactNode, path = '/') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>{component}</MemoryRouter>
    </QueryClientProvider>,
  )
}
it('shows server-provided targets and their detail navigation', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      respond({
        data: [
          {
            type: 'targets',
            id: 't-1',
            attributes: {
              name: 'External network',
              entries: [],
              revision: 1,
              updated_at: '2026-01-01T00:00:00Z',
            },
          },
        ],
      }),
    ),
  )
  renderPage(<TargetsPage />)
  const link = await screen.findByRole('link', { name: 'External network' })
  expect(link.getAttribute('href')).toBe('/targets/t-1')
})
it('explains that missing results do not imply closed ports', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(respond({ data: [], meta: { absence: 'unknown' } })),
  )
  renderPage(<ResultsPage />, '/results?run_id=run-1')
  await waitFor(() => expect(screen.getByText('No matching observations')).toBeTruthy())
  expect(screen.getByText(/does not establish that ports are closed/i)).toBeTruthy()
})

const run = {
  type: 'runs',
  id: 'run-1',
  attributes: {
    active: false,
    settled: true,
    phase: 'completed',
    created_at: '2026-01-01T00:00:00Z',
    target_id: 'target-1',
    scan_plan_id: 'plan-1',
    summary: {
      Found: 1,
      ShardCount: 1,
      CompletedShards: 1,
      ScanOutcome: 'complete',
      CleanupOutcome: 'complete',
    },
    preflight: {
      shard_count: 1,
      effective_workers: 1,
      config: { Workers: 1, Provider: 'vultr', Region: 'atl', InstanceType: 'vc2-1c-1gb' },
    },
    worker_snapshots: {},
  },
}

function renderRunDetail() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/runs/run-1']}>
        <Routes>
          <Route path="/runs/:id" element={<RunDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

it('offers CSV and JSONL for a completed run and shows a missing-file error', async () => {
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockImplementation((path: string) =>
        path.includes('/workers')
          ? Promise.resolve(respond({ data: [] }))
          : path.endsWith('/exports/csv')
            ? Promise.resolve(
                new Response(
                  JSON.stringify({ errors: [{ status: '409', detail: 'saved export missing' }] }),
                  { status: 409, headers: { 'Content-Type': 'application/vnd.api+json' } },
                ),
              )
            : Promise.resolve(respond({ data: run })),
      ),
  )
  renderRunDetail()
  const csv = await screen.findByRole('button', { name: 'Download CSV' })
  expect(csv.hasAttribute('disabled')).toBe(false)
  expect(screen.getByRole('button', { name: 'Download JSONL' }).hasAttribute('disabled')).toBe(
    false,
  )
  csv.focus()
  await userEvent.keyboard('{Enter}')
  expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'saved export missing')
})

it('blocks exports for incomplete runs and explains the coverage limit', async () => {
  const failed = {
    ...run,
    attributes: {
      ...run.attributes,
      phase: 'failed',
      summary: { ...run.attributes.summary, ScanOutcome: 'failed' },
    },
  }
  const fetch = vi
    .fn()
    .mockImplementation((path: string) =>
      Promise.resolve(respond(path.includes('/workers') ? { data: [] } : { data: failed })),
    )
  vi.stubGlobal('fetch', fetch)
  renderRunDetail()
  const csv = await screen.findByRole('button', { name: 'Download CSV' })
  expect(csv.hasAttribute('disabled')).toBe(true)
  expect(screen.getByText(/do not establish complete coverage/i)).toBeTruthy()
  fireEvent.click(csv)
  expect(fetch).not.toHaveBeenCalledWith(expect.stringContaining('/exports/'), expect.anything())
})

it('shows DigitalOcean worker details and links current and replacement instances', async () => {
  const digitalOceanRun = {
    ...run,
    attributes: {
      ...run.attributes,
      active: true,
      preflight: {
        ...run.attributes.preflight,
        config: {
          ...run.attributes.preflight.config,
          Provider: 'digitalocean',
          Region: 'tor1',
          InstanceType: 's-1vcpu-1gb',
        },
      },
      worker_snapshots: {
        '1': { slot: 1, id: '123', ip: '192.0.2.10', state: 'scanning', assigned_shards: 2 },
      },
    },
  }
  const history = {
    data: [
      {
        type: 'run-workers',
        id: '122',
        attributes: {
          slot: 1,
          ip: '192.0.2.9',
          state: 'failed',
          cleanup: 'complete',
          created_at: '2026-01-01T00:00:00Z',
        },
      },
      {
        type: 'run-workers',
        id: '123',
        attributes: {
          slot: 1,
          ip: '0.0.0.0',
          state: 'scanning',
          cleanup: 'pending',
          created_at: '2026-01-01T00:05:00Z',
        },
      },
    ],
  }
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockImplementation((path: string) =>
        Promise.resolve(respond(path.includes('/workers') ? history : { data: digitalOceanRun })),
      ),
  )
  renderRunDetail()
  const currentLinks = await screen.findAllByRole('link', {
    name: 'Open DigitalOcean instance 123',
  })
  expect(currentLinks).toHaveLength(2)
  for (const link of currentLinks) {
    expect(link.getAttribute('href')).toBe('https://cloud.digitalocean.com/droplets/123/')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  }
  const previous = screen.getByRole('link', { name: 'Open DigitalOcean instance 122' })
  expect(previous.getAttribute('href')).toBe('https://cloud.digitalocean.com/droplets/122/')
  const table = screen.getByRole('table')
  expect(within(table).getAllByText('DigitalOcean')).toHaveLength(2)
  expect(within(table).getAllByText('tor1')).toHaveLength(2)
  expect(within(table).getAllByText('s-1vcpu-1gb')).toHaveLength(2)
  expect(within(table).getByText('192.0.2.9')).toBeTruthy()
  expect(within(table).getByText('192.0.2.10')).toBeTruthy()
  expect(within(table).queryByText('0.0.0.0')).toBeNull()
})

it('shows Vultr and pending worker details without a generic provider link', async () => {
  const vultrRun = {
    ...run,
    attributes: {
      ...run.attributes,
      active: true,
      worker_snapshots: {
        '1': { slot: 1, id: 'worker-uuid', ip: '192.0.2.10', state: 'scanning' },
        '2': { slot: 2, id: '', ip: '0.0.0.0', state: 'provisioning' },
      },
    },
  }
  vi.stubGlobal(
    'fetch',
    vi.fn().mockImplementation((path: string) =>
      Promise.resolve(
        respond(
          path.includes('/workers')
            ? {
                data: [
                  {
                    type: 'run-workers',
                    id: 'worker-uuid',
                    attributes: {
                      slot: 1,
                      ip: '192.0.2.10',
                      state: 'scanning',
                      cleanup: 'pending',
                      created_at: '2026-01-01T00:00:00Z',
                    },
                  },
                ],
              }
            : { data: vultrRun },
        ),
      ),
    ),
  )
  renderRunDetail()
  expect(await screen.findByText('Worker 2')).toBeTruthy()
  expect(screen.getAllByText('worker-uuid')).toHaveLength(2)
  expect(screen.getAllByText('Pending').length).toBeGreaterThan(0)
  expect(screen.queryByText('0.0.0.0')).toBeNull()
  expect(screen.getAllByText('Vultr').length).toBeGreaterThan(0)
  expect(screen.queryByRole('link', { name: /open vultr instance/i })).toBeNull()
  expect(
    screen.queryAllByRole('link').some((link) => link.getAttribute('href')?.includes('vultr.com')),
  ).toBe(false)
})

it.each([
  ['digitalocean', '123/other'],
  ['other-provider', '123'],
])('does not link an unsupported worker identity (%s, %s)', async (provider, id) => {
  const unsafeRun = {
    ...run,
    attributes: {
      ...run.attributes,
      preflight: {
        ...run.attributes.preflight,
        config: { ...run.attributes.preflight.config, Provider: provider },
      },
      worker_snapshots: { '1': { slot: 1, id, ip: '192.0.2.10', state: 'scanning' } },
    },
  }
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockImplementation((path: string) =>
        Promise.resolve(respond(path.includes('/workers') ? { data: [] } : { data: unsafeRun })),
      ),
  )
  renderRunDetail()
  expect(await screen.findByText(id)).toBeTruthy()
  expect(screen.queryByRole('link', { name: /open .* instance/i })).toBeNull()
})
