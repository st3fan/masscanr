import { useState } from 'react'
import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { Plus } from 'lucide-react'
import { toast } from 'sonner'
import { Link, useParams } from 'react-router-dom'
import { ApiError, api, nextCursor, type ScanPlan } from '@/api/client'
import { queries } from '@/api/queries'
import { DataTable } from '@/components/data-table'
import { formatTime } from '@/components/domain'
import { PageHeader } from '@/components/layout'
import { useCursorPagination } from '@/components/pagination'
import { ErrorState, LoadingState } from '@/components/states'
import { TargetLink } from '@/components/target-link'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'

export async function invalidatePlanViews(client: QueryClient, id: string) {
  await Promise.all([
    client.invalidateQueries({ queryKey: ['plans'] }),
    client.invalidateQueries({ queryKey: ['plan', id] }),
    client.invalidateQueries({ queryKey: ['runs'] }),
  ])
}

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
  { header: 'Workers', cell: ({ row }) => row.original.attributes.settings.workers },
  {
    header: 'Status',
    cell: ({ row }) => (
      <Badge variant={row.original.attributes.archived_at ? 'secondary' : 'default'}>
        {row.original.attributes.archived_at ? 'Archived' : 'Active'}
      </Badge>
    ),
  },
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
        action={
          <Link to="/scan-plans/new" className={buttonVariants({})}>
            <Plus /> Create scan plan
          </Link>
        }
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
          emptyDescription="Create a scan plan for an active Target."
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
  const [archiveOpen, setArchiveOpen] = useState(false)
  const [archiveConflict, setArchiveConflict] = useState(false)
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['plan', id], queryFn: ({ signal }) => api.plan(id, signal) })
  const archive = useMutation({
    mutationFn: (revision: number) => api.archivePlan(id, revision),
    onSuccess: async () => {
      await invalidatePlanViews(queryClient, id)
      setArchiveOpen(false)
      toast.success('Scan plan archived')
    },
    onError: (error) => {
      setArchiveOpen(false)
      if (error instanceof ApiError && error.status === 409) setArchiveConflict(true)
    },
  })
  if (query.isPending) return <LoadingState />
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />
  const plan = query.data.data
  const settings = plan.attributes.settings
  const archived = !!plan.attributes.archived_at
  const fields = [
    ['Target', <TargetLink id={plan.attributes.target_id} />],
    ['Ports', settings.ports],
    ['Backend', settings.backend],
    ['Provider', settings.provider],
    ['Region', settings.region],
    ['Instance type', settings.instance_type],
    ['Requested workers', settings.workers],
    ['Rate', settings.rate],
    ['Scan timeout', settings.timeout],
    ['Cleanup timeout', settings.cleanup_timeout],
  ] as const
  return (
    <>
      <PageHeader
        eyebrow="Scan plan"
        title={plan.attributes.name}
        description={`Revision ${plan.attributes.revision} · Updated ${formatTime(plan.attributes.updated_at)} · ${archived ? 'Archived' : 'Active'}`}
        action={
          !archived && (
            <div className="flex gap-2">
              <Link
                to={`/scan-plans/${plan.id}/edit`}
                className={buttonVariants({ variant: 'outline' })}
              >
                Edit scan plan
              </Link>
              <Dialog open={archiveOpen} onOpenChange={setArchiveOpen}>
                <DialogTrigger render={<Button variant="destructive" />}>
                  Archive scan plan
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Archive this ScanPlan?</DialogTitle>
                    <DialogDescription>
                      {plan.attributes.name} will remain in history. Existing runs continue, but no
                      new run can start from this plan. Archiving cannot be undone.
                    </DialogDescription>
                  </DialogHeader>
                  <DialogFooter>
                    <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
                    <Button
                      variant="destructive"
                      disabled={archive.isPending}
                      onClick={() => archive.mutate(plan.attributes.revision)}
                    >
                      {archive.isPending ? 'Archiving…' : 'Archive scan plan'}
                    </Button>
                  </DialogFooter>
                </DialogContent>
              </Dialog>
            </div>
          )
        }
      />
      {archived && (
        <p role="status" className="mb-5 rounded-lg border bg-muted p-3 text-sm">
          Archived on {formatTime(plan.attributes.archived_at ?? undefined)}. This ScanPlan is
          read-only and retained for run history.
        </p>
      )}
      {archive.error && (
        <div
          role="alert"
          className="mb-5 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
        >
          {archiveConflict
            ? 'This ScanPlan changed before it could be archived. Reload it and review the latest version.'
            : archive.error.message}
          {archiveConflict && (
            <Button
              type="button"
              variant="outline"
              className="mt-3 block"
              onClick={() => {
                void query.refetch()
                setArchiveConflict(false)
                archive.reset()
              }}
            >
              Reload scan plan
            </Button>
          )}
        </div>
      )}
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
