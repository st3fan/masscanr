import { afterEach, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { PlanDetailPage } from './plans'

afterEach(() => {
  cleanup()
  sessionStorage.clear()
  vi.unstubAllGlobals()
})

const plan = {
  type: 'scan-plans',
  id: 'p-1',
  attributes: {
    name: 'Routine',
    target_id: 't-1',
    revision: 2,
    archived_at: null,
    updated_at: '2026-01-01T00:00:00Z',
    settings: {
      ports: '22,80',
      backend: 'masscan',
      provider: 'vultr',
      region: 'atl',
      instance_type: 'vc2-1c-1gb',
      workers: 2,
      rate: 1000,
      timeout: '24h',
      cleanup_timeout: '5m',
    },
  },
}

function preflight(expiresAt = '2099-01-01T00:00:00Z') {
  return {
    type: 'scan-preflights',
    id: 'pf-1',
    attributes: {
      target_id: 't-1',
      target_revision: 4,
      scan_plan_id: 'p-1',
      scan_plan_revision: 2,
      expires_at: expiresAt,
      effective_workers: 2,
      shard_count: 3,
      requires_confirmation: true,
      workload: { Addresses: 256, Ports: 2, Probes: 512 },
      config: {
        Backend: 'masscan',
        Provider: 'vultr',
        Region: 'atl',
        InstanceType: 'vc2-1c-1gb',
        Ports: [22, 80],
        Rate: 1000,
        Workers: 2,
        RequestedTargets: [
          {
            input: 'AS64500',
            target: 'AS64500',
            prefixes: ['192.0.2.0/24'],
            resolution: {
              source: 'RIPE',
              snapshot_at: '2026-01-01T00:00:00Z',
              fetched_at: '2026-01-01T00:01:00Z',
            },
          },
        ],
        Targets: ['192.0.2.0/24'],
      },
    },
  }
}

function reply(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/vnd.api+json' },
  })
}

function renderPlan() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/scan-plans/p-1']}>
        <Routes>
          <Route path="/scan-plans/:id" element={<PlanDetailPage />} />
          <Route path="/runs/:id" element={<div>Run detail reached</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

it('shows frozen requested and effective scope before deliberate confirmation, then navigates', async () => {
  const fetch = vi.fn().mockImplementation((path: string) => {
    if (path.endsWith('/preflight')) return Promise.resolve(reply({ data: preflight() }, 201))
    if (path.endsWith('/start'))
      return Promise.resolve(
        reply({ data: { type: 'runs', id: 'r-1', attributes: { phase: 'accepted' } } }, 202),
      )
    return Promise.resolve(reply({ data: plan }))
  })
  vi.stubGlobal('fetch', fetch)
  renderPlan()
  expect(await screen.findByRole('button', { name: 'Request fresh preflight' })).toBeTruthy()
  expect(
    fetch.mock.calls.some(([path]) => path.endsWith('/preflight') || path.endsWith('/start')),
  ).toBe(false)
  await userEvent.click(screen.getByRole('button', { name: 'Request fresh preflight' }))
  expect(await screen.findByText('Requested Target selectors')).toBeTruthy()
  expect(screen.getByText('AS64500')).toBeTruthy()
  expect(screen.getByText(/AS source: RIPE/)).toBeTruthy()
  expect(screen.getByText('Effective IPv4 scan scope')).toBeTruthy()
  expect(screen.getAllByText(/192\.0\.2\.0\/24/)).toHaveLength(2)
  expect(screen.getByText('512')).toBeTruthy()
  expect(fetch.mock.calls.some(([path]) => path.endsWith('/start'))).toBe(false)
  await userEvent.click(screen.getByRole('button', { name: 'Review and start scan' }))
  expect(fetch.mock.calls.some(([path]) => path.endsWith('/start'))).toBe(false)
  await userEvent.click(screen.getByRole('button', { name: 'Confirm and start scan' }))
  expect(await screen.findByText('Run detail reached')).toBeTruthy()
  const start = fetch.mock.calls.find(([path]) => path.endsWith('/start'))
  expect(start?.[1].headers['Idempotency-Key']).toBeTruthy()
  expect(JSON.parse(start?.[1].body).data.attributes).toEqual({
    preflight_id: 'pf-1',
    confirm: true,
  })
  expect(sessionStorage.getItem('masscanr:start:p-1')).toBeNull()
})

it('retains the same start attempt after an uncertain response and page refresh', async () => {
  let starts = 0
  const fetch = vi.fn().mockImplementation((path: string) => {
    if (path.endsWith('/preflight')) return Promise.resolve(reply({ data: preflight() }, 201))
    if (path.endsWith('/start')) {
      starts++
      return starts === 1
        ? Promise.reject(new TypeError('network connection lost'))
        : Promise.resolve(
            reply({ data: { type: 'runs', id: 'r-1', attributes: { phase: 'accepted' } } }, 202),
          )
    }
    return Promise.resolve(reply({ data: plan }))
  })
  vi.stubGlobal('fetch', fetch)
  renderPlan()
  await userEvent.click(await screen.findByRole('button', { name: 'Request fresh preflight' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Review and start scan' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm and start scan' }))
  expect(await screen.findByText(/Start outcome is uncertain/)).toBeTruthy()
  expect(sessionStorage.getItem('masscanr:start:p-1')).toContain('pf-1')
  cleanup()
  renderPlan()
  expect(await screen.findByText(/A start attempt needs a confirmed outcome/)).toBeTruthy()
  expect(fetch.mock.calls.filter(([path]) => path.endsWith('/start'))).toHaveLength(1)
  await userEvent.click(screen.getByRole('button', { name: 'Retry same start attempt' }))
  expect(await screen.findByText('Run detail reached')).toBeTruthy()
  const startCalls = fetch.mock.calls.filter(([path]) => path.endsWith('/start'))
  expect(startCalls).toHaveLength(2)
  expect(startCalls[1][1].headers['Idempotency-Key']).toBe(
    startCalls[0][1].headers['Idempotency-Key'],
  )
  expect(startCalls[1][1].body).toBe(startCalls[0][1].body)
  expect(sessionStorage.getItem('masscanr:start:p-1')).toBeNull()
})

it('explains a rejected start and requires a fresh preflight', async () => {
  const fetch = vi.fn().mockImplementation((path: string) => {
    if (path.endsWith('/preflight')) return Promise.resolve(reply({ data: preflight() }, 201))
    if (path.endsWith('/start'))
      return Promise.resolve(reply({ errors: [{ status: '409', detail: 'conflict' }] }, 409))
    return Promise.resolve(reply({ data: plan }))
  })
  vi.stubGlobal('fetch', fetch)
  renderPlan()
  await userEvent.click(await screen.findByRole('button', { name: 'Request fresh preflight' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Review and start scan' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm and start scan' }))
  expect(await screen.findByText(/preflight may be stale or expired/)).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Request fresh preflight' })).toBeTruthy()
  expect(sessionStorage.getItem('masscanr:start:p-1')).toBeNull()
  await waitFor(() =>
    expect(fetch.mock.calls.filter(([path]) => path.endsWith('/start'))).toHaveLength(1),
  )
})

it('does not submit an expired preflight', async () => {
  const fetch = vi
    .fn()
    .mockImplementation((path: string) =>
      Promise.resolve(
        reply(
          { data: path.endsWith('/preflight') ? preflight('2000-01-01T00:00:00Z') : plan },
          path.endsWith('/preflight') ? 201 : 200,
        ),
      ),
    )
  vi.stubGlobal('fetch', fetch)
  renderPlan()
  await userEvent.click(await screen.findByRole('button', { name: 'Request fresh preflight' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Review and start scan' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm and start scan' }))
  expect(await screen.findByText(/This preflight expired/)).toBeTruthy()
  expect(fetch.mock.calls.some(([path]) => path.endsWith('/start'))).toBe(false)
})

it('explains an archived Target or ScanPlan preflight conflict without starting', async () => {
  const fetch = vi
    .fn()
    .mockImplementation((path: string) =>
      Promise.resolve(
        path.endsWith('/preflight')
          ? reply({ errors: [{ status: '409', detail: 'conflict' }] }, 409)
          : reply({ data: plan }),
      ),
    )
  vi.stubGlobal('fetch', fetch)
  renderPlan()
  await userEvent.click(await screen.findByRole('button', { name: 'Request fresh preflight' }))
  expect(await screen.findByText(/ScanPlan or Target changed or was archived/)).toBeTruthy()
  expect(fetch.mock.calls.some(([path]) => path.endsWith('/start'))).toBe(false)
})

it('reports missing provider credentials and preserves the same attempt', async () => {
  const fetch = vi.fn().mockImplementation((path: string) => {
    if (path.endsWith('/preflight')) return Promise.resolve(reply({ data: preflight() }, 201))
    if (path.endsWith('/start'))
      return Promise.resolve(
        reply(
          { errors: [{ status: '503', detail: 'VULTR_API_TOKEN is required to start scans' }] },
          503,
        ),
      )
    return Promise.resolve(reply({ data: plan }))
  })
  vi.stubGlobal('fetch', fetch)
  renderPlan()
  await userEvent.click(await screen.findByRole('button', { name: 'Request fresh preflight' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Review and start scan' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm and start scan' }))
  expect(await screen.findByText(/Provider credentials are missing/)).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Retry same start attempt' })).toBeTruthy()
  expect(sessionStorage.getItem('masscanr:start:p-1')).toContain('pf-1')
})

it('blocks a preflight response that lacks effective scope', async () => {
  const incomplete = preflight()
  incomplete.attributes.config.Targets = []
  const fetch = vi
    .fn()
    .mockImplementation((path: string) =>
      Promise.resolve(
        reply(
          { data: path.endsWith('/preflight') ? incomplete : plan },
          path.endsWith('/preflight') ? 201 : 200,
        ),
      ),
    )
  vi.stubGlobal('fetch', fetch)
  renderPlan()
  await userEvent.click(await screen.findByRole('button', { name: 'Request fresh preflight' }))
  expect(await screen.findByText(/lacks reviewable scope or revisions/)).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Review and start scan' })).toBeNull()
  expect(fetch.mock.calls.some(([path]) => path.endsWith('/start'))).toBe(false)
})
