import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ApiError, api, nextCursor, type Target } from '@/api/client'
import { queries } from '@/api/queries'
import { DataTable } from '@/components/data-table'
import { formatTime } from '@/components/domain'
import { PageHeader } from '@/components/layout'
import { useCursorPagination } from '@/components/pagination'
import { ErrorState, LoadingState } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { Input } from '@/components/ui/input'
import { validateTargetForm } from './target-validation'

async function invalidateTargetViews(client: QueryClient, id: string) {
  await Promise.all([
    client.invalidateQueries({ queryKey: ['targets'] }),
    client.invalidateQueries({ queryKey: ['target', id] }),
    client.invalidateQueries({ queryKey: ['plans'] }),
    client.invalidateQueries({ queryKey: ['plan'] }),
  ])
}

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
        action={
          <Button render={<Link to="/targets/new" />}>
            <Plus /> Create target
          </Button>
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
          emptyTitle="No targets configured"
          emptyDescription="Create a Target to define authorized scope."
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

function TargetForm({ target, onReload }: { target?: Target; onReload?: () => Promise<void> }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [name, setName] = useState(target?.attributes.name ?? '')
  const [entries, setEntries] = useState<string[]>(
    target?.attributes.entries?.map((entry) => entry.input) ?? [''],
  )
  const [revision] = useState(target?.attributes.revision ?? 0)
  const [attempted, setAttempted] = useState(false)
  const [conflict, setConflict] = useState(false)
  const checked = validateTargetForm(name, entries)
  const mutation = useMutation({
    mutationFn: () =>
      target
        ? api.updateTarget(target.id, { revision, entries: checked.entries })
        : api.createTarget({ name: checked.name, entries: checked.entries }),
    onSuccess: async (result) => {
      await invalidateTargetViews(queryClient, result.data.id)
      toast.success(target ? 'Target updated' : 'Target created')
      navigate(`/targets/${result.data.id}`)
    },
    onError: (error) => {
      if (target && error instanceof ApiError && error.status === 409) setConflict(true)
    },
  })
  function updateEntry(index: number, value: string) {
    setEntries((current) => current.map((entry, position) => (position === index ? value : entry)))
  }
  function moveEntry(index: number, direction: -1 | 1) {
    setEntries((current) => {
      const moved = [...current]
      const entry = moved[index]
      moved[index] = moved[index + direction]
      moved[index + direction] = entry
      return moved
    })
  }
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setAttempted(true)
    if (checked.valid) mutation.mutate()
  }
  return (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>{target ? 'Edit authorized scope' : 'New authorized scope'}</CardTitle>
      </CardHeader>
      <CardContent>
        <form className="space-y-6" onSubmit={submit} noValidate>
          <div className="space-y-2">
            {target ? (
              <span className="text-sm font-medium">Name</span>
            ) : (
              <label htmlFor="target-name" className="text-sm font-medium">
                Name
              </label>
            )}
            {target ? (
              <p id="target-name" className="rounded-md border bg-muted px-3 py-2 text-sm">
                {name}
              </p>
            ) : (
              <Input
                id="target-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                aria-invalid={!!checked.nameError && (attempted || !!name)}
                aria-describedby={
                  (attempted || !!name) && checked.nameError
                    ? 'target-name-help target-name-error'
                    : 'target-name-help'
                }
                maxLength={200}
                autoComplete="off"
              />
            )}
            <p id="target-name-help" className="text-xs text-muted-foreground">
              Names are unique and cannot be changed after creation.
            </p>
            {!target && (attempted || !!name) && checked.nameError && (
              <p id="target-name-error" className="text-sm text-destructive">
                {checked.nameError}
              </p>
            )}
          </div>
          <div className="space-y-3">
            <div>
              <h2 className="text-sm font-medium">Selectors, in attribution order</h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Add only IPv4 addresses, CIDR ranges, or AS numbers your organization is authorized
                to scan. Order affects attribution when selectors overlap.
              </p>
            </div>
            {entries.map((entry, index) => {
              const error = checked.entryErrors[index]
              return (
                <div key={index} className="rounded-lg border p-3">
                  <label htmlFor={`selector-${index}`} className="mb-2 block text-sm font-medium">
                    Selector {index + 1}
                  </label>
                  <div className="flex flex-wrap gap-2 sm:flex-nowrap">
                    <Input
                      id={`selector-${index}`}
                      value={entry}
                      onChange={(event) => updateEntry(index, event.target.value)}
                      placeholder="192.0.2.0/24 or AS3333"
                      aria-invalid={!!error && (attempted || !!entry)}
                      aria-describedby={
                        error && (attempted || !!entry) ? `selector-error-${index}` : undefined
                      }
                      autoComplete="off"
                    />
                    <div className="flex gap-1">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        aria-label={`Move selector ${index + 1} up`}
                        disabled={index === 0}
                        onClick={() => moveEntry(index, -1)}
                      >
                        <ArrowUp />
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        aria-label={`Move selector ${index + 1} down`}
                        disabled={index === entries.length - 1}
                        onClick={() => moveEntry(index, 1)}
                      >
                        <ArrowDown />
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        aria-label={`Remove selector ${index + 1}`}
                        disabled={entries.length === 1}
                        onClick={() =>
                          setEntries((current) =>
                            current.filter((_, position) => position !== index),
                          )
                        }
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  </div>
                  {(attempted || !!entry) && error && (
                    <p id={`selector-error-${index}`} className="mt-2 text-sm text-destructive">
                      {error}
                    </p>
                  )}
                </div>
              )
            })}
            {attempted && checked.entriesError && (
              <p role="alert" className="text-sm text-destructive">
                {checked.entriesError}
              </p>
            )}
            <Button
              type="button"
              variant="outline"
              disabled={entries.length >= 1024}
              onClick={() => setEntries((current) => [...current, ''])}
            >
              <Plus /> Add selector
            </Button>
          </div>
          {mutation.error && (
            <div
              role="alert"
              className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
            >
              {conflict
                ? 'This Target changed since you opened it. Your draft is preserved. Reload the latest version before editing again.'
                : mutation.error.message}
              {conflict && onReload && (
                <Button
                  type="button"
                  className="mt-3 block"
                  variant="outline"
                  onClick={() => void onReload()}
                >
                  Reload latest version
                </Button>
              )}
            </div>
          )}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={mutation.isPending || conflict}>
              {mutation.isPending ? 'Saving…' : target ? 'Save target' : 'Create target'}
            </Button>
            <Button
              type="button"
              variant="outline"
              render={<Link to={target ? `/targets/${target.id}` : '/targets'} />}
            >
              Cancel
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

export function TargetNewPage() {
  return (
    <>
      <PageHeader
        eyebrow="Authorized scope"
        title="Create target"
        description="Name a reusable set of authorized network selectors."
      />
      <TargetForm />
    </>
  )
}

export function TargetEditPage() {
  const { id = '' } = useParams()
  const [version, setVersion] = useState(0)
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
        title={`Edit ${target.attributes.name}`}
        description="Changes use the revision loaded when this form opened."
      />
      {target.attributes.archived_at ? (
        <p role="alert">
          This Target is archived and cannot be edited.{' '}
          <Link className="text-primary underline" to={`/targets/${id}`}>
            View target
          </Link>
        </p>
      ) : (
        <TargetForm
          key={`${target.id}:${version}`}
          target={target}
          onReload={async () => {
            const latest = await query.refetch()
            if (latest.data) setVersion((current) => current + 1)
          }}
        />
      )}
    </>
  )
}

export function TargetDetailPage() {
  const { id = '' } = useParams()
  const [archiveOpen, setArchiveOpen] = useState(false)
  const [archiveConflict, setArchiveConflict] = useState(false)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['target', id],
    queryFn: ({ signal }) => api.target(id, signal),
  })
  const archive = useMutation({
    mutationFn: (revision: number) => api.archiveTarget(id, revision),
    onSuccess: async () => {
      await invalidateTargetViews(queryClient, id)
      setArchiveOpen(false)
      toast.success('Target archived')
    },
    onError: (error) => {
      setArchiveOpen(false)
      if (error instanceof ApiError && error.status === 409) setArchiveConflict(true)
    },
  })
  if (query.isPending) return <LoadingState />
  if (query.error) return <ErrorState error={query.error} retry={() => query.refetch()} />
  const target = query.data.data
  const archived = !!target.attributes.archived_at
  return (
    <>
      <PageHeader
        eyebrow="Target"
        title={target.attributes.name}
        description={`Revision ${target.attributes.revision} · Updated ${formatTime(target.attributes.updated_at)} · ${archived ? 'Archived' : 'Active'}`}
        action={
          !archived && (
            <div className="flex gap-2">
              <Button variant="outline" render={<Link to={`/targets/${target.id}/edit`} />}>
                Edit target
              </Button>
              <Dialog open={archiveOpen} onOpenChange={setArchiveOpen}>
                <DialogTrigger render={<Button variant="destructive" />}>
                  Archive target
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Archive this Target?</DialogTitle>
                    <DialogDescription>
                      {target.attributes.name} will remain visible in historical runs but cannot be
                      used for new ScanPlans or preflights. This cannot be undone here.
                    </DialogDescription>
                  </DialogHeader>
                  <DialogFooter>
                    <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
                    <Button
                      variant="destructive"
                      disabled={archive.isPending}
                      onClick={() => archive.mutate(target.attributes.revision)}
                    >
                      {archive.isPending ? 'Archiving…' : 'Archive target'}
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
          Archived on {formatTime(target.attributes.archived_at)}. This Target is retained for
          history.
        </p>
      )}
      {archive.error && (
        <div
          role="alert"
          className="mb-5 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
        >
          {archiveConflict
            ? 'This Target changed before it could be archived. Reload it and review the latest version.'
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
              Reload target
            </Button>
          )}
        </div>
      )}
      <div className="grid gap-5 xl:grid-cols-[2fr_1fr]">
        <Card>
          <CardHeader>
            <CardTitle>Authorized entries</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="divide-y">
              {target.attributes.entries?.map((entry) => (
                <div
                  key={entry.id}
                  className="flex flex-wrap items-center justify-between gap-4 py-3"
                >
                  <div className="min-w-0">
                    <div className="break-all font-mono text-sm">{entry.input}</div>
                    {entry.selector !== entry.input && (
                      <div className="text-xs text-muted-foreground">
                        Normalized: {entry.selector}
                      </div>
                    )}
                  </div>
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
                {archived ? 'Archived' : 'Active'}
              </span>
            </div>
          </CardContent>
        </Card>
      </div>
    </>
  )
}
