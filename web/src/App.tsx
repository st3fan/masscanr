import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { AppLayout, PageHeader } from '@/components/layout'
import { PatternsPage } from '@/pages/patterns'
import { Toaster } from '@/components/ui/sonner'

const client = new QueryClient({
  defaultOptions: { queries: { retry: 1, staleTime: 10_000, refetchOnWindowFocus: true } },
})
function Placeholder() {
  return <PageHeader title="Inventory overview" description="Dashboard data will appear here." />
}
export function App() {
  return (
    <QueryClientProvider client={client}>
      <BrowserRouter>
        <a
          href="#main-content"
          className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-background focus:p-3"
        >
          Skip to content
        </a>
        <Routes>
          <Route element={<AppLayout />}>
            <Route index element={<Placeholder />} />
            <Route path="patterns" element={<PatternsPage />} />
          </Route>
        </Routes>
        <Toaster />
      </BrowserRouter>
    </QueryClientProvider>
  )
}
