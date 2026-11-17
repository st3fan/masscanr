import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, cleanup, fireEvent } from '@testing-library/react'
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
    preflight: { shard_count: 1, effective_workers: 1, config: { Workers: 1 } },
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
