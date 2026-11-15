import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { useSearchParams, Link } from 'react-router-dom'
import { api, nextCursor, type ScanResult } from '@/api/client'
import { DataTable } from '@/components/data-table'
import { PageHeader } from '@/components/layout'
import { useCursorPagination } from '@/components/pagination'
import { ErrorState, LoadingState } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

const columns: ColumnDef<ScanResult>[] = [
  {
    id: 'ip',
    header: 'IP address',
    cell: ({ row }) => <span className="font-mono font-medium">{row.original.attributes.ip}</span>,
    enableSorting: true,
  },
  {
    id: 'port',
    header: 'Port',
    cell: ({ row }) => (
      <span className="font-mono">
        {row.original.attributes.port}/{row.original.attributes.protocol}
      </span>
    ),
    enableSorting: true,
  },
  {
    header: 'Run',
    cell: ({ row }) => (
      <Link
        to={`/runs/${row.original.attributes.run_id}`}
        className="font-mono text-primary hover:underline"
      >
        {row.original.attributes.run_id.slice(0, 12)}…
      </Link>
    ),
    enableSorting: false,
  },
  {
    header: 'Observations',
    cell: ({ row }) => row.original.attributes.observations,
    enableSorting: false,
  },
  {
    header: 'Coverage',
    cell: ({ row }) => (
      <Badge
        variant={row.original.attributes.coverage === 'complete' ? 'secondary' : 'outline'}
        className="capitalize"
      >
        {row.original.attributes.coverage}
      </Badge>
    ),
    enableSorting: false,
  },
]

type Filters = { run: string; target: string; ip: string; port: string }
export function ResultsPage() {
  const [search] = useSearchParams()
  const [draft, setDraft] = useState<Filters>(() => ({
    run: search.get('run_id') ?? '',
    target: '',
    ip: '',
    port: '',
  }))
  const [filters, setFilters] = useState(draft)
  const [sorting, setSorting] = useState<SortingState>([])
  const page = useCursorPagination()
  const sort = sorting[0] ? (sorting[0].desc ? '-' : '') + sorting[0].id : undefined
  const params = {
    'filter[run_id]': filters.run,
    'filter[target_id]': filters.target,
    'filter[ip]': filters.ip,
    'filter[port]': filters.port,
    sort,
    after: page.cursor,
    limit: 50,
  }
  const query = useQuery({
    queryKey: ['results', params],
    queryFn: ({ signal }) => api.results(params, signal),
  })
  const coverage = query.data?.meta?.coverage
  const runCoverage =
    coverage && typeof coverage === 'object'
      ? (coverage as { state?: string; run_phase?: string; journal_checkpoint?: number })
      : undefined
  return (
    <>
      <PageHeader
        eyebrow="Positive observations"
        title="Scan results"
        description="Open ports found by completed and partial scans. Missing findings do not imply closed ports."
      />
      <Card className="mb-5">
        <CardContent className="py-5">
          <form
            className="grid gap-3 sm:grid-cols-2 xl:grid-cols-[1fr_1fr_1fr_120px_auto]"
            onSubmit={(event) => {
              event.preventDefault()
              setFilters(draft)
              page.reset()
            }}
          >
            <label className="text-xs font-medium text-muted-foreground">
              Run ID
              <Input
                className="mt-1.5"
                value={draft.run}
                onChange={(event) => setDraft({ ...draft, run: event.target.value.trim() })}
                placeholder="Any run"
              />
            </label>
            <label className="text-xs font-medium text-muted-foreground">
              Target ID
              <Input
                className="mt-1.5"
                value={draft.target}
                onChange={(event) => setDraft({ ...draft, target: event.target.value.trim() })}
                placeholder="Any target"
              />
            </label>
            <label className="text-xs font-medium text-muted-foreground">
              IPv4 address
              <Input
                className="mt-1.5"
                value={draft.ip}
                onChange={(event) => setDraft({ ...draft, ip: event.target.value.trim() })}
                placeholder="192.0.2.1"
              />
            </label>
            <label className="text-xs font-medium text-muted-foreground">
              TCP port
              <Input
                className="mt-1.5"
                type="number"
                min="0"
                max="65535"
                value={draft.port}
                onChange={(event) => setDraft({ ...draft, port: event.target.value })}
                placeholder="443"
              />
            </label>
            <Button type="submit" className="self-end">
              Apply filters
            </Button>
          </form>
        </CardContent>
      </Card>
      <div className="mb-4 rounded-lg border bg-muted/40 px-4 py-3 text-sm text-muted-foreground">
        {runCoverage ? (
          <>
            Run coverage:{' '}
            <strong className="text-foreground">{runCoverage.state ?? 'Unknown'}</strong> · Phase:{' '}
            {runCoverage.run_phase ?? 'Unknown'} · Journal checkpoint:{' '}
            {runCoverage.journal_checkpoint ?? 'Unknown'}.{' '}
          </>
        ) : null}
        Results represent positive open observations only. Coverage may be partial or uncertain.
      </div>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : (
        <DataTable
          data={query.data.data}
          columns={columns}
          emptyTitle="No matching observations"
          emptyDescription="No positive findings match these filters. This does not establish that ports are closed."
          previous={page.hasPrevious}
          next={!!nextCursor(query.data.links?.next)}
          onPrevious={page.previous}
          onNext={() => {
            const cursor = nextCursor(query.data.links?.next)
            if (cursor) page.next(cursor)
          }}
          sort={sorting}
          onSort={(value) => {
            setSorting(value.slice(0, 1))
            page.reset()
          }}
        />
      )}
    </>
  )
}
