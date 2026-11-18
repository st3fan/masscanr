import { afterEach, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { TargetDetailPage, TargetEditPage, TargetNewPage } from './targets'
import { validateTargetForm } from './target-validation'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})
const target = (revision = 1, archived = false) => ({
  type: 'targets',
  id: 't-1',
  attributes: {
    name: 'External network',
    revision,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...(archived ? { archived_at: '2026-01-02T00:00:00Z' } : {}),
    entries: [{ id: 'e-1', position: 0, kind: 'ip', input: '192.0.2.1', selector: '192.0.2.1/32' }],
  },
})
function reply(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/vnd.api+json' },
  })
}
function renderRoute(path: string, element: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path={path} element={element} />
          <Route path="/targets/:id" element={<div>Target saved</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

it('detects canonical duplicate selectors and invalid input before sending', () => {
  const result = validateTargetForm('External', ['192.0.2.1', '192.0.2.1/32', 'AS003333', 'as3333'])
  expect(result.entryErrors[1]).toMatch(/Duplicates selector 1/)
  expect(result.entryErrors[3]).toMatch(/Duplicates selector 3/)
  expect(validateTargetForm('External', ['192.0.2.1/24', '192.0.2.0/24']).entryErrors[1]).toMatch(
    /Duplicates selector 1/,
  )
  expect(validateTargetForm('External', ['2001:db8::1']).valid).toBe(false)
})

it('creates a Target with ordered selectors and navigates to its detail', async () => {
  const fetch = vi.fn().mockImplementation((path: string, options?: RequestInit) => {
    if (path === '/v1/targets' && options?.method === 'POST')
      return Promise.resolve(reply({ data: target() }, 201))
    return Promise.resolve(reply({ data: [] }))
  })
  vi.stubGlobal('fetch', fetch)
  renderRoute('/targets/new', <TargetNewPage />)
  await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'External network')
  await userEvent.type(screen.getByRole('textbox', { name: 'Selector 1' }), '192.0.2.1')
  await userEvent.click(screen.getByRole('button', { name: 'Add selector' }))
  await userEvent.type(screen.getByRole('textbox', { name: 'Selector 2' }), 'AS3333')
  await userEvent.click(screen.getByRole('button', { name: 'Create target' }))
  await screen.findByText('Target saved')
  const call = fetch.mock.calls.find(
    ([path, options]) => path === '/v1/targets' && options?.method === 'POST',
  )
  expect(JSON.parse(call?.[1].body)).toEqual({
    data: {
      type: 'targets',
      attributes: { name: 'External network', entries: ['192.0.2.1', 'AS3333'] },
    },
  })
})

it('blocks a duplicate before submission', async () => {
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  renderRoute('/targets/new', <TargetNewPage />)
  await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'External network')
  await userEvent.type(screen.getByRole('textbox', { name: 'Selector 1' }), '192.0.2.1')
  await userEvent.click(screen.getByRole('button', { name: 'Add selector' }))
  await userEvent.type(screen.getByRole('textbox', { name: 'Selector 2' }), '192.0.2.1/32')
  expect(screen.getByText(/Duplicates selector 1/)).toBeTruthy()
  await userEvent.click(screen.getByRole('button', { name: 'Create target' }))
  expect(fetch).not.toHaveBeenCalled()
})

it('keeps a stale edit draft until the operator reloads', async () => {
  let revision = 1
  const fetch = vi.fn().mockImplementation((_path: string, options?: RequestInit) => {
    if (options?.method === 'PATCH')
      return Promise.resolve(reply({ errors: [{ status: '409', detail: 'conflict' }] }, 409))
    return Promise.resolve(reply({ data: target(revision) }))
  })
  vi.stubGlobal('fetch', fetch)
  renderRoute('/targets/t-1/edit', <TargetEditPage />)
  await screen.findByText('External network')
  expect(screen.queryByRole('textbox', { name: 'Name' })).toBeNull()
  const selector = screen.getByRole('textbox', { name: 'Selector 1' })
  await userEvent.clear(selector)
  await userEvent.type(selector, '192.0.2.2')
  await userEvent.click(screen.getByRole('button', { name: 'Save target' }))
  expect(await screen.findByText(/Your draft is preserved/)).toBeTruthy()
  expect(screen.getByRole('textbox', { name: 'Selector 1' })).toHaveProperty('value', '192.0.2.2')
  const patch = fetch.mock.calls.find(([, options]) => options?.method === 'PATCH')
  expect(JSON.parse(patch?.[1].body).data.attributes).toEqual({
    revision: 1,
    entries: ['192.0.2.2'],
  })
  revision = 2
  await userEvent.click(screen.getByRole('button', { name: 'Reload latest version' }))
  await waitFor(() =>
    expect(screen.getByRole('textbox', { name: 'Selector 1' })).toHaveProperty(
      'value',
      '192.0.2.1',
    ),
  )
})

it('requires confirmation before archiving and shows the retired state', async () => {
  let archived = false
  const fetch = vi.fn().mockImplementation((_path: string, options?: RequestInit) => {
    if (options?.method === 'PATCH') {
      archived = true
      return Promise.resolve(reply({ data: target(2, true) }))
    }
    return Promise.resolve(reply({ data: target(archived ? 2 : 1, archived) }))
  })
  vi.stubGlobal('fetch', fetch)
  renderRoute('/targets/t-1', <TargetDetailPage />)
  await userEvent.click(await screen.findByRole('button', { name: 'Archive target' }))
  expect(screen.getByText(/cannot be used for new ScanPlans/)).toBeTruthy()
  expect(fetch.mock.calls.some(([, options]) => options?.method === 'PATCH')).toBe(false)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(screen.getByRole('button', { name: 'Archive target' }))
  await userEvent.click(screen.getByRole('button', { name: 'Archive target' }))
  await screen.findByText(/This Target is retained for history/)
  const patch = fetch.mock.calls.find(([, options]) => options?.method === 'PATCH')
  expect(JSON.parse(patch?.[1].body).data.attributes).toEqual({ revision: 1, archived: true })
})
