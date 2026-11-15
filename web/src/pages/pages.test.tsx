import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TargetsPage } from './targets'
import { ResultsPage } from './results'

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
