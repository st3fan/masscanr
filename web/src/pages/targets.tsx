import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { Link, useParams } from 'react-router-dom'
import { api, nextCursor, type Target } from '@/api/client'
import { queries } from '@/api/queries'
import { DataTable } from '@/components/data-table'
import { formatTime } from '@/components/domain'
import { PageHeader } from '@/components/layout'
import { useCursorPagination } from '@/components/pagination'
import { ErrorState, LoadingState } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

const columns: ColumnDef<Target>[] = [
  {
    header: 'Target',
    accessorKey: 'attributes.name',
    cell: ({ row }) => (
      <Link className="font-medium text-primary hover:underline" to={`/targets/${row.original.id}`}>
        {row.original.attributes.name}
      </Link>
    ),
  },
  { header: 'Entries', cell: ({ row }) => row.original.attributes.entries?.length ?? 0 },
  { header: 'Revision', cell: ({ row }) => row.original.attributes.revision },
  {
    header: 'Status',
    cell: ({ row }) => (
      <Badge variant={row.original.attributes.archived_at ? 'secondary' : 'default'}>
        {row.original.attributes.archived_at ? 'Archived' : 'Active'}
      </Badge>
    ),
  },
  { header: 'Updated', cell: ({ row }) => formatTime(row.original.attributes.updated_at) },
]
export function TargetsPage() {
  const page = useCursorPagination()
  const query = useQuery(queries.targets(50, page.cursor))
  return (
    <>
      <PageHeader
        eyebrow="Authorized scope"
        title="Targets"
        description="Reusable collections of IP addresses, CIDR ranges, and AS selectors."
      />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} retry={() => query.refetch()} />
      ) : (
        <DataTable
          data={query.data.data}
          columns={columns}
          emptyTitle="No targets configured"
          emptyDescription="Create targets through the API or CLI to see them here."
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
export function TargetDetailPage() {
  const { id = '' } = useParams()
  const query = useQuery({
    queryKey: ['target', id],
    queryFn: ({ signal }) => api.target(id, signal),
  })
  if (query.isPending) return <LoadingState />
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />
  const target = query.data.data
  return (
    <>
      <PageHeader
        eyebrow="Target"
        title={target.attributes.name}
        description={`Revision ${target.attributes.revision} · Updated ${formatTime(target.attributes.updated_at)}`}
      />
      <div className="grid gap-5 xl:grid-cols-[2fr_1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Authorized entries</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="divide-y">
              {target.attributes.entries?.map((entry) => (
                <div key={entry.id} className="flex items-center justify-between gap-4 py-3">
                  <div className="font-mono text-sm">{entry.input}</div>
                  <Badge variant="outline" className="uppercase">
                    {entry.kind}
                  </Badge>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Details</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div>
              ID <code className="block break-all text-muted-foreground">{target.id}</code>
            </div>
            <div>
              Created{' '}
              <span className="block text-muted-foreground">
                {formatTime(target.attributes.created_at)}
              </span>
            </div>
            <div>
              Status{' '}
              <span className="block text-muted-foreground">
                {target.attributes.archived_at ? 'Archived' : 'Active'}
              </span>
            </div>
          </CardContent>
        </Card>
      </div>
    </>
  )
}
