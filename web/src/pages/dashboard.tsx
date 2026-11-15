import { useQuery } from '@tanstack/react-query'
import { Activity, Blocks, CircleDot, Target } from 'lucide-react'
import { Link } from 'react-router-dom'
import { queries } from '@/api/queries'
import { api } from '@/api/client'
import { MetricCard, StatusBadge, formatTime } from '@/components/domain'
import { PageHeader } from '@/components/layout'
import { LoadingState, ErrorState, EmptyState } from '@/components/states'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

export function DashboardPage() {
  const targets = useQuery(queries.targets(100))
  const plans = useQuery(queries.plans(100))
  const runs = useQuery(queries.runs(8))
  const active = useQuery(queries.activeRuns())
  const health = useQuery({
    queryKey: ['health'],
    queryFn: ({ signal }) => api.ping(signal),
    refetchInterval: 15000,
    retry: 0,
  })
  return (
    <>
      <PageHeader
        eyebrow="Network inventory"
        title="Overview"
        description="Configured scope, scan plans, and recent execution activity from the private server."
      />
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          label="Targets"
          value={targets.data?.data.length ?? '—'}
          detail="Shown from first 100"
          icon={<Target className="size-4" />}
        />
        <MetricCard
          label="Scan plans"
          value={plans.data?.data.length ?? '—'}
          detail="Shown from first 100"
          icon={<Blocks className="size-4" />}
        />
        <MetricCard
          label="Active runs"
          value={active.data?.data.length ?? '—'}
          detail="Currently owned by server"
          icon={<Activity className="size-4" />}
        />
        <MetricCard
          label="Server status"
          value={health.isPending ? 'Checking' : health.error ? 'Unavailable' : health.data}
          detail={health.error ? health.error.message : 'Private API readiness'}
          icon={<CircleDot className="size-4" />}
        />
      </div>
      <div className="mt-6 grid gap-5 xl:grid-cols-[1.5fr_1fr]">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>Recent scan runs</CardTitle>
            <Button variant="outline" size="sm" render={<Link to="/runs" />}>
              View all
            </Button>
          </CardHeader>
          <CardContent>
            {runs.isPending ? (
              <LoadingState />
            ) : runs.error ? (
              <ErrorState error={runs.error} retry={() => runs.refetch()} />
            ) : runs.data.data.length ? (
              <div className="divide-y">
                {runs.data.data.map((run) => (
                  <Link
                    key={run.id}
                    to={`/runs/${run.id}`}
                    className="flex items-center justify-between gap-4 py-3 hover:text-primary"
                  >
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium">{run.id}</div>
                      <div className="text-xs text-muted-foreground">
                        {formatTime(run.attributes.created_at)}
                      </div>
                    </div>
                    <StatusBadge phase={run.attributes.phase} active={run.attributes.active} />
                  </Link>
                ))}
              </div>
            ) : (
              <EmptyState
                title="No runs yet"
                description="Run history appears after a scan is started through the API or CLI."
              />
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Operational context</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            <div className="flex justify-between border-b pb-3">
              <span className="text-muted-foreground">Access</span>
              <span>Private listener</span>
            </div>
            <div className="flex justify-between border-b pb-3">
              <span className="text-muted-foreground">Dashboard</span>
              <span>Read only</span>
            </div>
            <p className="text-muted-foreground">
              Results are positive open-port observations. Missing findings never prove a port is
              closed; check each run’s coverage before interpreting them.
            </p>
            <Button variant="outline" render={<Link to="/results" />}>
              Explore results
            </Button>
          </CardContent>
        </Card>
      </div>
    </>
  )
}
