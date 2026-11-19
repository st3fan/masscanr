import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  ApiError,
  api,
  type Provider,
  type ScanPlan,
  type ScanPlanSettings,
  type Target,
  type UpdateScanPlanAttributes,
} from '@/api/client'
import { queries } from '@/api/queries'
import { PageHeader } from '@/components/layout'
import { ErrorState, LoadingState } from '@/components/states'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { invalidatePlanViews } from './plans'

type Draft = {
  name: string
  targetName: string
  backend: string
  ports: string
  provider: Provider
  region: string
  instanceType: string
  workers: string
  rate: string
  timeout: string
  cleanupTimeout: string
}

const defaultDraft: Draft = {
  name: '',
  targetName: '',
  backend: 'masscan',
  ports: '22,80,443',
  provider: 'vultr',
  region: '',
  instanceType: '',
  workers: '1',
  rate: '1000',
  timeout: '24h',
  cleanupTimeout: '5m',
}

function settingsFromDraft(draft: Draft): ScanPlanSettings {
  return {
    backend: draft.backend,
    ports: draft.ports.trim(),
    provider: draft.provider,
    region: draft.region,
    instance_type: draft.instanceType,
    workers: Number(draft.workers),
    rate: Number(draft.rate),
    timeout: draft.timeout.trim(),
    cleanup_timeout: draft.cleanupTimeout.trim(),
  }
}

function validPositiveInteger(value: string) {
  return /^\d+$/.test(value) && Number.isSafeInteger(Number(value)) && Number(value) > 0
}

function Field({
  id,
  label,
  value,
  onChange,
  type = 'text',
  hint,
}: {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  type?: string
  hint?: string
}) {
  return (
    <div className="space-y-2">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      <Input
        id={id}
        type={type}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        autoComplete="off"
      />
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

const selectClass =
  'flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50'

function PlanForm({
  plan,
  target,
  onReload,
}: {
  plan?: ScanPlan
  target?: Target
  onReload?: () => Promise<void>
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const saved = plan?.attributes.settings
  const [draft, setDraft] = useState<Draft>(() =>
    plan && saved
      ? {
          name: plan.attributes.name,
          targetName: target?.attributes.name ?? '',
          backend: saved.backend,
          ports: saved.ports,
          provider: saved.provider as Provider,
          region: saved.region,
          instanceType: saved.instance_type,
          workers: String(saved.workers),
          rate: String(saved.rate),
          timeout: saved.timeout,
          cleanupTimeout: saved.cleanup_timeout,
        }
      : defaultDraft,
  )
  const [attempted, setAttempted] = useState(false)
  const [conflict, setConflict] = useState(false)
  const targets = useQuery(queries.targets(1000))
  const vultrRegions = useQuery({
    queryKey: ['catalog', 'regions', 'vultr'],
    queryFn: ({ signal }) => api.regions('vultr', signal),
  })
  const digitalOceanRegions = useQuery({
    queryKey: ['catalog', 'regions', 'digitalocean'],
    queryFn: ({ signal }) => api.regions('digitalocean', signal),
  })
  const regions = draft.provider === 'vultr' ? vultrRegions : digitalOceanRegions
  const instanceTypes = useQuery({
    queryKey: ['catalog', 'instance-types', draft.provider, draft.region],
    queryFn: ({ signal }) => api.instanceTypes(draft.provider, draft.region, signal),
    enabled: !!regions.data && !!draft.region,
  })
  const regionOptions = regions.data?.data ?? []
  const instanceOptions = instanceTypes.data?.data ?? []
  const placementChanged =
    !plan ||
    draft.provider !== saved?.provider ||
    draft.region !== saved.region ||
    draft.instanceType !== saved.instance_type
  const placementReady =
    !placementChanged ||
    (!!regions.data &&
      !!instanceTypes.data &&
      regionOptions.some((region) => region.id === draft.region) &&
      instanceOptions.some((instance) => instance.id === draft.instanceType))

  function change<K extends keyof Draft>(key: K, value: Draft[K]) {
    setDraft((current) => ({ ...current, [key]: value }))
  }

  const validation = (() => {
    if (!draft.name.trim()) return 'Enter a ScanPlan name.'
    if (!draft.targetName.trim()) return 'Choose an active Target by name.'
    if (!draft.ports.trim()) return 'Enter at least one TCP port.'
    if (!validPositiveInteger(draft.workers)) return 'Workers must be a positive whole number.'
    if (!validPositiveInteger(draft.rate)) return 'Rate must be a positive whole number.'
    if (!draft.timeout.trim() || !draft.cleanupTimeout.trim())
      return 'Enter both scan and cleanup timeouts.'
    if (!draft.region || !draft.instanceType || !placementReady)
      return 'Choose a region and instance type from the selected provider catalog.'
    return ''
  })()

  const mutation = useMutation({
    mutationFn: async () => {
      const name = draft.name.trim()
      const targetName = draft.targetName.trim()
      const settings = settingsFromDraft(draft)
      if (!plan || targetName !== target?.attributes.name) {
        const selected = await api.targetByName(targetName)
        if (selected.data.attributes.archived_at)
          throw new ApiError(409, 'The selected Target is archived. Choose an active Target.')
      }
      if (!plan) return api.createPlan({ name, target_name: targetName, settings })
      const changes: UpdateScanPlanAttributes = { revision: plan.attributes.revision }
      if (name !== plan.attributes.name) changes.name = name
      if (targetName !== target?.attributes.name) changes.target_name = targetName
      const changedSettings: NonNullable<UpdateScanPlanAttributes['settings']> = {}
      for (const key of Object.keys(settings) as (keyof ScanPlanSettings)[]) {
        if (settings[key] !== saved?.[key]) {
          // The generated update contract accepts partial settings.
          Object.assign(changedSettings, { [key]: settings[key] })
        }
      }
      if (Object.keys(changedSettings).length) changes.settings = changedSettings
      if (Object.keys(changes).length === 1) throw new Error('No changes to save.')
      return api.updatePlan(plan.id, changes)
    },
    onSuccess: async (result) => {
      await invalidatePlanViews(queryClient, result.data.id)
      toast.success(plan ? 'Scan plan updated' : 'Scan plan created')
      navigate(`/scan-plans/${result.data.id}`)
    },
    onError: (error) => {
      if (
        plan &&
        error instanceof ApiError &&
        error.status === 409 &&
        !error.message.startsWith('The selected Target is archived')
      )
        setConflict(true)
    },
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setAttempted(true)
    if (!validation) mutation.mutate()
  }

  return (
    <Card className="max-w-4xl">
      <CardHeader>
        <CardTitle>{plan ? 'Edit scan plan' : 'New scan plan'}</CardTitle>
      </CardHeader>
      <CardContent>
        <form className="space-y-6" onSubmit={submit} noValidate>
          <div className="grid gap-5 sm:grid-cols-2">
            <Field
              id="plan-name"
              label="Name"
              value={draft.name}
              onChange={(v) => change('name', v)}
            />
            <div className="space-y-2">
              <label htmlFor="plan-target" className="text-sm font-medium">
                Active Target name
              </label>
              <Input
                id="plan-target"
                list="active-target-names"
                value={draft.targetName}
                onChange={(event) => change('targetName', event.target.value)}
                autoComplete="off"
              />
              <datalist id="active-target-names">
                {targets.data?.data
                  .filter((item) => !item.attributes.archived_at)
                  .map((item) => (
                    <option key={item.id} value={item.attributes.name} />
                  ))}
              </datalist>
              <p className="text-xs text-muted-foreground">
                Choose a saved active Target or type its exact name. Scope is managed on the Target.
              </p>
              {targets.error && (
                <p className="text-xs text-muted-foreground">
                  Target suggestions are unavailable. You can still enter an exact Target name.
                </p>
              )}
            </div>
            <div className="space-y-2">
              <label htmlFor="plan-backend" className="text-sm font-medium">
                Scanner backend
              </label>
              <select
                id="plan-backend"
                className={selectClass}
                value={draft.backend}
                onChange={(event) => change('backend', event.target.value)}
              >
                <option value="masscan">masscan</option>
                <option value="zmap">ZMap</option>
              </select>
            </div>
            <Field
              id="plan-ports"
              label="TCP ports"
              value={draft.ports}
              onChange={(v) => change('ports', v)}
              hint="Comma-separated ports or ranges, such as 22,80,443 or 1-1024."
            />
            <div className="space-y-2">
              <label htmlFor="plan-provider" className="text-sm font-medium">
                Worker provider
              </label>
              <select
                id="plan-provider"
                className={selectClass}
                value={draft.provider}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    provider: event.target.value as Provider,
                    region: '',
                    instanceType: '',
                  }))
                }
              >
                <option value="vultr" disabled={!vultrRegions.data && draft.provider !== 'vultr'}>
                  Vultr
                </option>
                <option
                  value="digitalocean"
                  disabled={!digitalOceanRegions.data && draft.provider !== 'digitalocean'}
                >
                  DigitalOcean
                </option>
              </select>
              {(vultrRegions.error || digitalOceanRegions.error) && (
                <p className="text-xs text-muted-foreground">
                  Providers with unavailable catalogs cannot be newly selected.
                </p>
              )}
            </div>
            <div className="space-y-2">
              <label htmlFor="plan-region" className="text-sm font-medium">
                Region
              </label>
              <select
                id="plan-region"
                className={selectClass}
                value={draft.region}
                disabled={!regions.data}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    region: event.target.value,
                    instanceType: '',
                  }))
                }
              >
                <option value="">Choose a region</option>
                {draft.region && !regionOptions.some((item) => item.id === draft.region) && (
                  <option value={draft.region}>{draft.region} (saved)</option>
                )}
                {regionOptions.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.attributes.name} ({item.id})
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-2">
              <label htmlFor="plan-instance" className="text-sm font-medium">
                Instance type
              </label>
              <select
                id="plan-instance"
                className={selectClass}
                value={draft.instanceType}
                disabled={!instanceTypes.data}
                onChange={(event) => change('instanceType', event.target.value)}
              >
                <option value="">Choose an instance type</option>
                {draft.instanceType &&
                  !instanceOptions.some((item) => item.id === draft.instanceType) && (
                    <option value={draft.instanceType}>{draft.instanceType} (saved)</option>
                  )}
                {instanceOptions.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.id} · {item.attributes.vcpus} vCPU · {item.attributes.memory_mb} MB
                  </option>
                ))}
              </select>
            </div>
            <Field
              id="plan-workers"
              label="Requested workers"
              type="number"
              value={draft.workers}
              onChange={(v) => change('workers', v)}
              hint="Temporary workers are billable when a run starts."
            />
            <Field
              id="plan-rate"
              label="Probe rate"
              type="number"
              value={draft.rate}
              onChange={(v) => change('rate', v)}
            />
            <Field
              id="plan-timeout"
              label="Scan timeout"
              value={draft.timeout}
              onChange={(v) => change('timeout', v)}
              hint="Go duration, such as 24h or 30m."
            />
            <Field
              id="plan-cleanup-timeout"
              label="Cleanup timeout"
              value={draft.cleanupTimeout}
              onChange={(v) => change('cleanupTimeout', v)}
              hint="Go duration, such as 5m."
            />
          </div>
          {(regions.error || instanceTypes.error) && (
            <p role="status" className="rounded-lg border bg-muted p-3 text-sm">
              The selected provider catalog is unavailable. Saved placement remains visible; other
              settings can still be edited. New provider or placement choices require the catalog.
            </p>
          )}
          {attempted && validation && (
            <p role="alert" className="text-sm text-destructive">
              {validation}
            </p>
          )}
          {mutation.error && (
            <div
              role="alert"
              className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
            >
              {conflict
                ? 'This ScanPlan changed since you opened it, or the selected Target is archived. Your draft is preserved. Reload before editing again.'
                : mutation.error.message}
              {conflict && onReload && (
                <Button
                  type="button"
                  variant="outline"
                  className="mt-3 block"
                  onClick={() => void onReload()}
                >
                  Reload latest version
                </Button>
              )}
            </div>
          )}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={mutation.isPending || conflict}>
              {mutation.isPending ? 'Saving…' : plan ? 'Save scan plan' : 'Create scan plan'}
            </Button>
            <Link
              to={plan ? `/scan-plans/${plan.id}` : '/scan-plans'}
              className={buttonVariants({ variant: 'outline' })}
            >
              Cancel
            </Link>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

export function PlanNewPage() {
  return (
    <>
      <PageHeader
        eyebrow="Scan configuration"
        title="Create scan plan"
        description="Choose an authorized Target and review execution settings before saving. Saving does not start a scan."
      />
      <PlanForm />
    </>
  )
}

export function PlanEditPage() {
  const { id = '' } = useParams()
  const [version, setVersion] = useState(0)
  const planQuery = useQuery({
    queryKey: ['plan', id],
    queryFn: ({ signal }) => api.plan(id, signal),
  })
  const targetID = planQuery.data?.data.attributes.target_id ?? ''
  const targetQuery = useQuery({
    queryKey: ['target', targetID],
    queryFn: ({ signal }) => api.target(targetID, signal),
    enabled: !!targetID,
  })
  if (planQuery.error)
    return <ErrorState error={planQuery.error} retry={() => planQuery.refetch()} />
  if (planQuery.isPending || targetQuery.isPending) return <LoadingState />
  if (targetQuery.error)
    return <ErrorState error={targetQuery.error} retry={() => targetQuery.refetch()} />
  const plan = planQuery.data.data
  return (
    <>
      <PageHeader
        eyebrow="Scan plan"
        title={`Edit ${plan.attributes.name}`}
        description="Changes use the revision loaded when this form opened."
      />
      {plan.attributes.archived_at ? (
        <p role="alert">
          This ScanPlan is archived and cannot be edited.{' '}
          <Link className="text-primary underline" to={`/scan-plans/${id}`}>
            View scan plan
          </Link>
        </p>
      ) : (
        <PlanForm
          key={`${plan.id}:${version}`}
          plan={plan}
          target={targetQuery.data.data}
          onReload={async () => {
            const latest = await planQuery.refetch()
            if (latest.data) {
              setVersion((current) => current + 1)
            }
          }}
        />
      )}
    </>
  )
}
