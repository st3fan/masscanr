import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { Link, useParams } from 'react-router-dom'
import { Download, ExternalLink } from 'lucide-react'
import {
  api,
  downloadRunExport,
  nextCursor,
  type ExportFormat,
  type RunWorker,
  type ScanRun,
} from '@/api/client'
import { queries } from '@/api/queries'
import { DataTable } from '@/components/data-table'
import { MetricCard, StatusBadge, formatTime } from '@/components/domain'
import { PageHeader } from '@/components/layout'
import { useCursorPagination } from '@/components/pagination'
import { ErrorState, LoadingState } from '@/components/states'
import { TargetLink } from '@/components/target-link'
import { Button } from '@/components/ui/button'
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

function providerName(provider: string) {
  switch (provider) {
    case 'digitalocean':
      return 'DigitalOcean'
    case 'vultr':
      return 'Vultr'
    default:
      return provider || 'Unknown'
  }
}

function instanceURL(provider: string, id: string) {
  if (provider === 'digitalocean' && /^[1-9]\d*$/.test(id)) {
    return `https://cloud.digitalocean.com/droplets/${id}/`
  }
  return null
}

function knownWorkerIP(ip: string | undefined) {
  return ip && ip !== '0.0.0.0' ? ip : null
}

function WorkerInstance({ provider, id }: { provider: string; id: string }) {
  if (!id) return <span className="text-muted-foreground">Pending</span>
  const url = instanceURL(provider, id)
  if (!url) return <code className="break-all text-xs">{id}</code>
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      aria-label={`Open ${providerName(provider)} instance ${id}`}
      className="inline-flex max-w-full items-center gap-1 text-primary hover:underline"
    >
      <code className="break-all text-xs">{id}</code>
      <ExternalLink aria-hidden="true" className="size-3 shrink-0" />
    </a>
  )
}

function workerColumns(
  provider: string,
  region: string,
  flavour: string,
  currentIPs: Map<string, string>,
): ColumnDef<RunWorker>[] {
  return [
    {
      header: 'Instance',
      cell: ({ row }) => <WorkerInstance provider={provider} id={row.original.id} />,
    },
    { header: 'Worker slot', cell: ({ row }) => row.original.attributes.slot },
    { header: 'Provider', cell: () => providerName(provider) },
    {
      header: 'IP',
      cell: ({ row }) =>
        knownWorkerIP(row.original.attributes.ip) ?? currentIPs.get(row.original.id) ?? 'Unknown',
    },
    { header: 'Region', cell: () => region || 'Unknown' },
    { header: 'Flavour', cell: () => flavour || 'Unknown' },
    { header: 'State', cell: ({ row }) => <StatusBadge phase={row.original.attributes.state} /> },
    { header: 'Cleanup', cell: ({ row }) => row.original.attributes.cleanup },
    { header: 'Created', cell: ({ row }) => formatTime(row.original.attributes.created_at) },
  ]
}
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
  const [downloading, setDownloading] = useState<ExportFormat | null>(null)
  const [downloadError, setDownloadError] = useState<string | null>(null)
  const historyPage = useCursorPagination()
  const query = useQuery({
    queryKey: ['run', id],
    queryFn: ({ signal }) => api.run(id, signal),
    refetchInterval: (data) => (data.state.data?.data.attributes.active ? 5000 : false),
  })
  const history = useQuery({
    ...queries.runWorkers(id, 100, historyPage.cursor),
    refetchInterval: query.data?.data.attributes.active ? 5000 : false,
  })
  if (query.isPending) return <LoadingState />
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />
  const run = query.data.data
  const a = run.attributes
  const provider = a.preflight.config.Provider
  const region = a.preflight.config.Region
  const flavour = a.preflight.config.InstanceType
  const workers = Object.entries(a.worker_snapshots ?? {}).sort(
    ([, left], [, right]) => (left.slot ?? 0) - (right.slot ?? 0),
  )
  const currentIPs = new Map<string, string>()
  for (const [, worker] of workers) {
    const ip = knownWorkerIP(worker.ip)
    if (worker.id && ip) currentIPs.set(worker.id, ip)
  }
  const totalShards = a.summary.ShardCount || a.preflight.shard_count
  const completedShards = a.summary.CompletedShards
  const shardPercent = totalShards > 0 ? (100 * completedShards) / totalShards : 0
  const canExport = !a.active && a.settled && a.summary.ScanOutcome === 'complete'
  async function download(format: ExportFormat) {
    setDownloading(format)
    setDownloadError(null)
    try {
      await downloadRunExport(run.id, format)
    } catch (error) {
      setDownloadError(error instanceof Error ? error.message : 'Export download failed')
    } finally {
      setDownloading(null)
    }
  }
  return (
    <>
      <PageHeader
        eyebrow="Scan run"
        title={run.id}
        description={`Created ${formatTime(a.created_at)} · ${a.active ? 'Active' : 'Settled'}`}
        action={<StatusBadge phase={a.phase} active={a.active} />}
      />
      <div className="grid gap-4 sm:grid-cols-4">
        <MetricCard
          label="Open observations"
          value={a.summary.Found}
          detail="Positive findings only"
        />
        <MetricCard
          label="Shards completed"
          value={`${completedShards}/${totalShards}`}
          detail={`${shardPercent.toFixed(1)}% of logical shards`}
        />
        <MetricCard
          label="Workers"
          value={a.preflight.effective_workers}
          detail={`${a.preflight.config.Workers} requested`}
        />
        <MetricCard
          label="Cleanup"
          value={a.summary.CleanupOutcome || 'Pending'}
          detail={a.summary.ReconciliationUnresolved ? 'Reconciliation unresolved' : undefined}
        />
      </div>
      <div className="mt-5 space-y-2">
        <div className="flex justify-between text-sm">
          <span className="font-medium">Total shard progress</span>
          <span className="text-muted-foreground">
            {completedShards}/{totalShards} completed
          </span>
        </div>
        <Progress value={shardPercent} aria-label="Total shard progress" />
      </div>
      {a.error && (
        <div
          role="alert"
          className="mt-5 rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive"
        >
          {a.error}
        </div>
      )}
      <Card className="mt-6">
        <CardHeader>
          <CardTitle>Download results</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm text-muted-foreground">
            {canExport
              ? 'Download the saved results for this completed run.'
              : a.active
                ? 'Downloads become available after the scan completes.'
                : 'A complete export is unavailable for this run. Positive findings may exist, but they do not establish complete coverage.'}
          </p>
          <div className="flex flex-wrap gap-2">
            {(['csv', 'jsonl'] as const).map((format) => (
              <Button
                key={format}
                type="button"
                variant="outline"
                disabled={!canExport || downloading !== null}
                onClick={() => void download(format)}
              >
                <Download aria-hidden="true" />
                {downloading === format
                  ? `Downloading ${format.toUpperCase()}…`
                  : `Download ${format.toUpperCase()}`}
              </Button>
            ))}
          </div>
          {downloadError && (
            <p role="alert" className="text-sm text-destructive">
              {downloadError}
            </p>
          )}
        </CardContent>
      </Card>
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
                      <span className="font-medium">Worker {worker.slot || key}</span>
                      <span className="capitalize text-muted-foreground">
                        {worker.state || 'Unknown'}
                      </span>
                    </div>
                    <dl className="grid gap-x-4 gap-y-2 text-xs sm:grid-cols-2 xl:grid-cols-3">
                      <div className="min-w-0">
                        <dt className="text-muted-foreground">Provider</dt>
                        <dd>{providerName(provider)}</dd>
                      </div>
                      <div className="min-w-0">
                        <dt className="text-muted-foreground">IP</dt>
                        <dd className="break-all">{knownWorkerIP(worker.ip) ?? 'Pending'}</dd>
                      </div>
                      <div className="min-w-0">
                        <dt className="text-muted-foreground">Region</dt>
                        <dd>{region || 'Unknown'}</dd>
                      </div>
                      <div className="min-w-0">
                        <dt className="text-muted-foreground">Flavour</dt>
                        <dd>{flavour || 'Unknown'}</dd>
                      </div>
                      <div className="min-w-0">
                        <dt className="text-muted-foreground">Instance</dt>
                        <dd>
                          <WorkerInstance provider={provider} id={worker.id} />
                        </dd>
                      </div>
                    </dl>
                    <div className="flex flex-wrap justify-between gap-2 text-xs text-muted-foreground">
                      <span>
                        {worker.completed_shards ?? 0}/{worker.assigned_shards ?? 0} assigned shards
                        completed
                      </span>
                      <span>
                        {worker.current_shard
                          ? `Shard ${worker.current_shard} · attempt ${worker.attempt ?? 1}`
                          : 'No shard in progress'}
                      </span>
                    </div>
                    <Progress
                      value={
                        worker.assigned_shards
                          ? (100 * (worker.completed_shards ?? 0)) / worker.assigned_shards
                          : 0
                      }
                      aria-label={`Worker ${key} completed shards`}
                    />
                    <p className="text-xs text-muted-foreground">
                      {worker.current_shard && worker.progress && !worker.progress.PercentUnknown
                        ? `Current shard ${worker.progress.Percent.toFixed(1)}% complete`
                        : worker.current_shard
                          ? 'Current shard progress unknown'
                          : 'Waiting for next shard or finished'}
                      {worker.instance_count && worker.instance_count > 1
                        ? ` · ${worker.instance_count} physical instances`
                        : ''}
                    </p>
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
              <span className="block">
                {a.target_id ? <TargetLink id={a.target_id} /> : 'Unknown'}
              </span>
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
      <Card className="mt-6">
        <CardHeader>
          <CardTitle>Physical worker history</CardTitle>
        </CardHeader>
        <CardContent>
          {history.isPending ? (
            <LoadingState />
          ) : history.error ? (
            <ErrorState error={history.error} retry={() => history.refetch()} />
          ) : (
            <DataTable
              data={history.data.data}
              columns={workerColumns(provider, region, flavour, currentIPs)}
              emptyTitle="No instances recorded"
              emptyDescription="Worker instances appear after provisioning begins."
              previous={historyPage.hasPrevious}
              next={!!nextCursor(history.data.links?.next)}
              onPrevious={historyPage.previous}
              onNext={() => {
                const cursor = nextCursor(history.data.links?.next)
                if (cursor) historyPage.next(cursor)
              }}
            />
          )}
        </CardContent>
      </Card>
    </>
  )
}
