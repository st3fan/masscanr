import { lazy, Suspense } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Link, Route, Routes } from 'react-router-dom'
import { ThemeProvider } from 'next-themes'
import { AppLayout, PageHeader } from '@/components/layout'
import { LoadingState } from '@/components/states'
import { Toaster } from '@/components/ui/sonner'

const DashboardPage = lazy(() =>
  import('@/pages/dashboard').then((m) => ({ default: m.DashboardPage })),
)
const TargetsPage = lazy(() => import('@/pages/targets').then((m) => ({ default: m.TargetsPage })))
const TargetDetailPage = lazy(() =>
  import('@/pages/targets').then((m) => ({ default: m.TargetDetailPage })),
)
const PlansPage = lazy(() => import('@/pages/plans').then((m) => ({ default: m.PlansPage })))
const PlanDetailPage = lazy(() =>
  import('@/pages/plans').then((m) => ({ default: m.PlanDetailPage })),
)
const RunsPage = lazy(() => import('@/pages/runs').then((m) => ({ default: m.RunsPage })))
const RunDetailPage = lazy(() => import('@/pages/runs').then((m) => ({ default: m.RunDetailPage })))
const ResultsPage = lazy(() => import('@/pages/results').then((m) => ({ default: m.ResultsPage })))
const PatternsPage = lazy(() =>
  import('@/pages/patterns').then((m) => ({ default: m.PatternsPage })),
)

const client = new QueryClient({
  defaultOptions: { queries: { retry: 1, staleTime: 10_000, refetchOnWindowFocus: true } },
})
export function App() {
  return (
    <QueryClientProvider client={client}>
      <ThemeProvider
        attribute="class"
        defaultTheme="system"
        enableSystem
        storageKey="masscanr-theme"
      >
        <BrowserRouter>
          <a
            href="#main-content"
            className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-background focus:p-3"
          >
            Skip to content
          </a>
          <Suspense
            fallback={
              <div className="p-8">
                <LoadingState />
              </div>
            }
          >
            <Routes>
              <Route element={<AppLayout />}>
                <Route index element={<DashboardPage />} />
                <Route path="targets" element={<TargetsPage />} />
                <Route path="targets/:id" element={<TargetDetailPage />} />
                <Route path="scan-plans" element={<PlansPage />} />
                <Route path="scan-plans/:id" element={<PlanDetailPage />} />
                <Route path="runs" element={<RunsPage />} />
                <Route path="runs/:id" element={<RunDetailPage />} />
                <Route path="results" element={<ResultsPage />} />
                <Route path="patterns" element={<PatternsPage />} />
                <Route
                  path="*"
                  element={
                    <>
                      <PageHeader
                        title="Page not found"
                        description="This dashboard route does not exist."
                      />
                      <Link className="text-primary hover:underline" to="/">
                        Return to overview
                      </Link>
                    </>
                  }
                />
              </Route>
            </Routes>
          </Suspense>
          <Toaster />
        </BrowserRouter>
      </ThemeProvider>
    </QueryClientProvider>
  )
}
