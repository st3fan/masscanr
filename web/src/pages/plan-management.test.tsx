import { afterEach, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { PlanEditPage, PlanNewPage } from './plan-form'
import { PlanDetailPage } from './plans'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

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
const target = {
  type: 'targets',
  id: 't-1',
  attributes: { name: 'External network', archived_at: null },
}
const plan = (revision = 1, archived = false) => ({
  type: 'scan-plans',
  id: 'p-1',
  attributes: {
    name: 'Routine',
    target_id: 't-1',
    revision,
    archived_at: archived ? '2026-01-02T00:00:00Z' : null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    settings,
  },
})
function reply(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/vnd.api+json' },
  })
}
function catalog(path: string) {
  if (path === '/v1/catalog/regions?provider=vultr')
    return reply({ data: [{ type: 'regions', id: 'atl', attributes: { name: 'Atlanta' } }] })
  if (path === '/v1/catalog/regions?provider=digitalocean')
    return reply({ data: [{ type: 'regions', id: 'tor1', attributes: { name: 'Toronto' } }] })
  if (path === '/v1/catalog/instance-types?provider=vultr&region=atl')
    return reply({
      data: [
        { type: 'instance-types', id: 'vc2-1c-1gb', attributes: { vcpus: 1, memory_mb: 1024 } },
      ],
    })
  if (path === '/v1/catalog/instance-types?provider=digitalocean&region=tor1')
    return reply({
      data: [
        { type: 'instance-types', id: 's-1vcpu-1gb', attributes: { vcpus: 1, memory_mb: 1024 } },
      ],
    })
  return undefined
}
function renderRoute(path: string, element: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const route =
    path === '/scan-plans/new'
      ? path
      : path.endsWith('/edit')
        ? '/scan-plans/:id/edit'
        : '/scan-plans/:id'
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path={route} element={element} />
          {route !== '/scan-plans/:id' && (
            <Route path="/scan-plans/:id" element={<div>Scan plan saved</div>} />
          )}
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

it('creates a plan from an active Target and catalog choices without starting a scan', async () => {
  const fetch = vi.fn().mockImplementation((path: string, options?: RequestInit) => {
    if (catalog(path)) return Promise.resolve(catalog(path))
    if (path === '/v1/targets') return Promise.resolve(reply({ data: [target] }))
    if (path === '/v1/targets/by-name/External%20network')
      return Promise.resolve(reply({ data: target }))
    if (path === '/v1/scan-plans' && options?.method === 'POST')
      return Promise.resolve(reply({ data: plan() }, 201))
    return Promise.resolve(reply({ data: [] }))
  })
  vi.stubGlobal('fetch', fetch)
  renderRoute('/scan-plans/new', <PlanNewPage />)
  await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'Routine')
  await userEvent.type(
    screen.getByRole('combobox', { name: 'Active Target name' }),
    'External network',
  )
  await screen.findByRole('option', { name: 'Atlanta (atl)' })
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Region' }), 'atl')
  await screen.findByRole('option', { name: /vc2-1c-1gb/ })
  await userEvent.selectOptions(
    screen.getByRole('combobox', { name: 'Instance type' }),
    'vc2-1c-1gb',
  )
  await userEvent.click(screen.getByRole('button', { name: 'Create scan plan' }))
  await screen.findByText('Scan plan saved')
  const post = fetch.mock.calls.find(
    ([path, options]) => path === '/v1/scan-plans' && options?.method === 'POST',
  )
  expect(JSON.parse(post?.[1].body).data.attributes).toEqual({
    name: 'Routine',
    target_name: 'External network',
    settings,
  })
  expect(fetch.mock.calls.some(([path]) => path.endsWith('/start'))).toBe(false)
})

it('clears old placement when switching providers', async () => {
  const fetch = vi.fn().mockImplementation((path: string, options?: RequestInit) => {
    if (catalog(path)) return Promise.resolve(catalog(path))
    if (path === '/v1/targets') return Promise.resolve(reply({ data: [target] }))
    if (path === '/v1/targets/t-1') return Promise.resolve(reply({ data: target }))
    if (path === '/v1/scan-plans/p-1' && options?.method === 'PATCH')
      return Promise.resolve(reply({ data: plan(2) }))
    return Promise.resolve(reply({ data: plan() }))
  })
  vi.stubGlobal('fetch', fetch)
  renderRoute('/scan-plans/p-1/edit', <PlanEditPage />)
  await screen.findByRole('combobox', { name: 'Worker provider' })
  await waitFor(() =>
    expect(screen.getByRole('option', { name: 'DigitalOcean' })).toHaveProperty('disabled', false),
  )
  await userEvent.selectOptions(
    screen.getByRole('combobox', { name: 'Worker provider' }),
    'digitalocean',
  )
  expect(screen.getByRole('combobox', { name: 'Region' })).toHaveProperty('value', '')
  await screen.findByRole('option', { name: 'Toronto (tor1)' })
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Region' }), 'tor1')
  await screen.findByRole('option', { name: /s-1vcpu-1gb/ })
  await userEvent.selectOptions(
    screen.getByRole('combobox', { name: 'Instance type' }),
    's-1vcpu-1gb',
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save scan plan' }))
  await screen.findByText('Scan plan saved')
  const patch = fetch.mock.calls.find(([, options]) => options?.method === 'PATCH')
  expect(JSON.parse(patch?.[1].body).data.attributes).toEqual({
    revision: 1,
    settings: { provider: 'digitalocean', region: 'tor1', instance_type: 's-1vcpu-1gb' },
  })
})

it('keeps saved placement and allows an unrelated edit when its catalog fails', async () => {
  const fetch = vi.fn().mockImplementation((path: string, options?: RequestInit) => {
    if (path === '/v1/catalog/regions?provider=vultr')
      return Promise.resolve(
        reply({ errors: [{ status: '503', detail: 'catalog unavailable' }] }, 503),
      )
    if (catalog(path)) return Promise.resolve(catalog(path))
    if (path === '/v1/targets') return Promise.resolve(reply({ data: [target] }))
    if (path === '/v1/targets/t-1') return Promise.resolve(reply({ data: target }))
    if (path === '/v1/scan-plans/p-1' && options?.method === 'PATCH')
      return Promise.resolve(reply({ data: plan(2) }))
    return Promise.resolve(reply({ data: plan() }))
  })
  vi.stubGlobal('fetch', fetch)
  renderRoute('/scan-plans/p-1/edit', <PlanEditPage />)
  await screen.findByText(/The selected provider catalog is unavailable/)
  expect(screen.getByRole('combobox', { name: 'Region' })).toHaveProperty('value', 'atl')
  expect(screen.getByRole('combobox', { name: 'Region' })).toHaveProperty('disabled', true)
  await userEvent.clear(screen.getByRole('textbox', { name: 'Name' }))
  await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'Evening')
  await userEvent.click(screen.getByRole('button', { name: 'Save scan plan' }))
  await screen.findByText('Scan plan saved')
  const patch = fetch.mock.calls.find(([, options]) => options?.method === 'PATCH')
  expect(JSON.parse(patch?.[1].body).data.attributes).toEqual({ revision: 1, name: 'Evening' })
})

it('preserves a draft after a revision conflict', async () => {
  const fetch = vi.fn().mockImplementation((path: string, options?: RequestInit) => {
    if (catalog(path)) return Promise.resolve(catalog(path))
    if (path === '/v1/targets') return Promise.resolve(reply({ data: [target] }))
    if (path === '/v1/targets/t-1') return Promise.resolve(reply({ data: target }))
    if (path === '/v1/scan-plans/p-1' && options?.method === 'PATCH')
      return Promise.resolve(reply({ errors: [{ status: '409', detail: 'conflict' }] }, 409))
    return Promise.resolve(reply({ data: plan() }))
  })
  vi.stubGlobal('fetch', fetch)
  renderRoute('/scan-plans/p-1/edit', <PlanEditPage />)
  const name = await screen.findByRole('textbox', { name: 'Name' })
  await userEvent.clear(name)
  await userEvent.type(name, 'Evening')
  await userEvent.click(screen.getByRole('button', { name: 'Save scan plan' }))
  expect(await screen.findByText(/Your draft is preserved/)).toBeTruthy()
  expect(name).toHaveProperty('value', 'Evening')
  expect(screen.getByRole('button', { name: 'Save scan plan' })).toHaveProperty('disabled', true)
})

it('requires confirmation before archiving and then shows a read-only plan', async () => {
  let archived = false
  const fetch = vi.fn().mockImplementation((path: string, options?: RequestInit) => {
    if (path === '/v1/scan-plans/p-1' && options?.method === 'PATCH') {
      archived = true
      return Promise.resolve(reply({ data: plan(2, true) }))
    }
    if (path === '/v1/targets/t-1') return Promise.resolve(reply({ data: target }))
    return Promise.resolve(reply({ data: plan(archived ? 2 : 1, archived) }))
  })
  vi.stubGlobal('fetch', fetch)
  renderRoute('/scan-plans/p-1', <PlanDetailPage />)
  await userEvent.click(await screen.findByRole('button', { name: 'Archive scan plan' }))
  expect(screen.getByText(/Existing runs continue/)).toBeTruthy()
  expect(fetch.mock.calls.some(([, options]) => options?.method === 'PATCH')).toBe(false)
  await userEvent.click(screen.getAllByRole('button', { name: 'Archive scan plan' }).at(-1)!)
  await screen.findByText(/This ScanPlan is read-only/)
  expect(screen.queryByRole('button', { name: 'Edit scan plan' })).toBeNull()
  const patch = fetch.mock.calls.find(([, options]) => options?.method === 'PATCH')
  expect(JSON.parse(patch?.[1].body).data.attributes).toEqual({ revision: 1, archived: true })
  await waitFor(() => expect(fetch).toHaveBeenCalled())
})
