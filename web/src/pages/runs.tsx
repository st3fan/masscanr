import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { Link, useParams } from 'react-router-dom'
import { api, nextCursor, type ScanRun } from '@/api/client'
import { queries } from '@/api/queries'
import { DataTable } from '@/components/data-table'
import { MetricCard, StatusBadge, formatTime } from '@/components/domain'
import { PageHeader } from '@/components/layout'
import { useCursorPagination } from '@/components/pagination'
import { ErrorState, LoadingState } from '@/components/states'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'

const columns: ColumnDef<ScanRun>[] = [
  {
    header: 'Run',
    cell: ({ row }) => (
      <Link
        to={`/runs/${row.original.id}`}
        className="font-mono text-sm font-medium text-primary hover:underline"
      >
        {row.original.id.slice(0, 12)}…
      </Link>
    ),
  },
  {
    header: 'Status',
    cell: ({ row }) => (
      <StatusBadge phase={row.original.attributes.phase} active={row.original.attributes.active} />
    ),
  },
  {
    header: 'Scan plan',
    cell: ({ row }) =>
      row.original.attributes.scan_plan_id ? (
        <Link
          to={`/scan-plans/${row.original.attributes.scan_plan_id}`}
          className="text-primary hover:underline"
        >
          View plan
        </Link>
      ) : (
        '—'
      ),
  },
  { header: 'Findings', cell: ({ row }) => row.original.attributes.summary.Found },
  { header: 'Created', cell: ({ row }) => formatTime(row.original.attributes.created_at) },
]
export function RunsPage() {
  const page = useCursorPagination()
  const query = useQuery(queries.runs(50, page.cursor))
  return (
    <>
      <PageHeader
        eyebrow="Execution history"
        title="Scan runs"
        description="Recent execution snapshots; active runs refresh automatically."
      />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : (
        <DataTable
          data={query.data.data}
          columns={columns}
          emptyTitle="No scan runs yet"
          emptyDescription="Run history will appear after a scan is started through the API or CLI."
          previous={page.hasPrevious}
          next={!!nextCursor(query.data.links?.next)}
          onPrevious={page.previous}
          onNext={() => {
            const cursor = nextCursor(query.data.links?.next)
            if (cursor) page.next(cursor)
          }}
        />
      )}
    </>
  )
}
export function RunDetailPage() {
  const { id = '' } = useParams()
  const query = useQuery({
    queryKey: ['run', id],
    queryFn: ({ signal }) => api.run(id, signal),
    refetchInterval: (data) => (data.state.data?.data.attributes.active ? 5000 : false),
  })
  if (query.isPending) return <LoadingState />
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />
  const run = query.data.data
  const a = run.attributes
  const workers = Object.entries(a.worker_snapshots ?? {})
  return (
    <>
      <PageHeader
        eyebrow="Scan run"
        title={run.id}
        description={`Created ${formatTime(a.created_at)} · ${a.active ? 'Active' : 'Settled'}`}
        action={<StatusBadge phase={a.phase} active={a.active} />}
      />
      <div className="grid gap-4 sm:grid-cols-3">
        <MetricCard
          label="Open observations"
          value={a.summary.Found}
          detail="Positive findings only"
        />
        <MetricCard label="Workers" value={workers.length} detail="Reported worker snapshots" />
        <MetricCard
          label="Cleanup"
          value={a.summary.CleanupOutcome || 'Pending'}
          detail={a.summary.ReconciliationUnresolved ? 'Reconciliation unresolved' : undefined}
        />
      </div>
      {a.error && (
        <div
          role="alert"
          className="mt-5 rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive"
        >
          {a.error}
        </div>
      )}
      <div className="mt-6 grid gap-5 xl:grid-cols-[2fr_1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Workers and progress</CardTitle>
          </CardHeader>
          <CardContent>
            {workers.length ? (
              <div className="divide-y">
                {workers.map(([key, worker]) => (
                  <div key={key} className="space-y-2 py-3">
                    <div className="flex justify-between gap-3 text-sm">
                      <span className="font-medium">Shard {worker.shard || key}</span>
                      <span className="capitalize text-muted-foreground">
                        {worker.state || 'Unknown'}
                      </span>
                    </div>
                    {worker.progress && !worker.progress.RateUnknown ? (
                      <Progress
                        value={Math.min(100, Math.max(0, worker.progress.Percent))}
                        aria-label={`Shard ${key} progress`}
                      />
                    ) : (
                      <p className="text-xs text-muted-foreground">Progress unknown</p>
                    )}
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">No worker activity reported yet.</p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Effective scope</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div>
              Target{' '}
              <Link
                className="block break-all text-primary hover:underline"
                to={`/targets/${a.target_id}`}
              >
                {a.target_id || 'Unknown'}
              </Link>
            </div>
            <div>
              Scan plan{' '}
              <Link
                className="block break-all text-primary hover:underline"
                to={`/scan-plans/${a.scan_plan_id}`}
              >
                {a.scan_plan_id || 'Unknown'}
              </Link>
            </div>
            <div>
              Outcome{' '}
              <span className="block capitalize text-muted-foreground">
                {a.summary.ScanOutcome || 'Pending'}
              </span>
            </div>
            <Link
              className="text-primary hover:underline"
              to={`/results?run_id=${encodeURIComponent(run.id)}`}
            >
              View positive results →
            </Link>
          </CardContent>
        </Card>
      </div>
    </>
  )
}
