import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { Link, useParams } from 'react-router-dom'
import { api, nextCursor, type ScanPlan } from '@/api/client'
import { queries } from '@/api/queries'
import { DataTable } from '@/components/data-table'
import { formatTime } from '@/components/domain'
import { PageHeader } from '@/components/layout'
import { useCursorPagination } from '@/components/pagination'
import { ErrorState, LoadingState } from '@/components/states'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

const columns: ColumnDef<ScanPlan>[] = [
  {
    header: 'Scan plan',
    cell: ({ row }) => (
      <Link
        to={`/scan-plans/${row.original.id}`}
        className="font-medium text-primary hover:underline"
      >
        {row.original.attributes.name}
      </Link>
    ),
  },
  { header: 'Ports', cell: ({ row }) => <code>{row.original.attributes.settings.ports}</code> },
  { header: 'Backend', cell: ({ row }) => row.original.attributes.settings.backend },
  { header: 'Workers', cell: ({ row }) => row.original.attributes.settings.shards },
  { header: 'Revision', cell: ({ row }) => row.original.attributes.revision },
  { header: 'Updated', cell: ({ row }) => formatTime(row.original.attributes.updated_at) },
]
export function PlansPage() {
  const page = useCursorPagination()
  const query = useQuery(queries.plans(50, page.cursor))
  return (
    <>
      <PageHeader
        eyebrow="Scan configuration"
        title="Scan plans"
        description="Reusable execution settings associated with an authorized target."
      />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : (
        <DataTable
          data={query.data.data}
          columns={columns}
          emptyTitle="No scan plans configured"
          emptyDescription="Create a scan plan through the API or CLI to see it here."
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
export function PlanDetailPage() {
  const { id = '' } = useParams()
  const query = useQuery({ queryKey: ['plan', id], queryFn: ({ signal }) => api.plan(id, signal) })
  if (query.isPending) return <LoadingState />
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />
  const plan = query.data.data
  const settings = plan.attributes.settings
  const fields = [
    [
      'Target',
      <Link to={`/targets/${plan.attributes.target_id}`} className="text-primary hover:underline">
        {plan.attributes.target_id}
      </Link>,
    ],
    ['Ports', settings.ports],
    ['Backend', settings.backend],
    ['Provider', settings.provider],
    ['Region', settings.region],
    ['Instance type', settings.instance_type],
    ['Workers / shards', settings.shards],
    ['Rate', settings.rate],
    ['Scan timeout', settings.timeout],
    ['Cleanup timeout', settings.cleanup_timeout],
  ] as const
  return (
    <>
      <PageHeader
        eyebrow="Scan plan"
        title={plan.attributes.name}
        description={`Revision ${plan.attributes.revision} · Updated ${formatTime(plan.attributes.updated_at)}`}
      />
      <Card>
        <CardHeader>
          <CardTitle>Configuration</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid gap-x-8 gap-y-5 sm:grid-cols-2 xl:grid-cols-3">
            {fields.map(([label, value]) => (
              <div key={label}>
                <dt className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  {label}
                </dt>
                <dd className="mt-1 break-all text-sm font-medium">{value}</dd>
              </div>
            ))}
          </dl>
        </CardContent>
      </Card>
    </>
  )
}
