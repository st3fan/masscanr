import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { ApiError, api, type ScanPlan, type ScanPreflight } from '@/api/client'
import { formatTime } from '@/components/domain'
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
} from '@/components/ui/dialog'

type StartAttempt = { preflightId: string; key: string }

function newIdempotencyKey() {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

function reviewablePreflight(preflight: ScanPreflight, planId: string): boolean {
  const a = preflight.attributes
  const cfg = a?.config
  return (
    !!preflight.id &&
    !!a?.target_id &&
    Number.isInteger(a.target_revision) &&
    a.scan_plan_id === planId &&
    Number.isInteger(a.scan_plan_revision) &&
    Number.isFinite(Date.parse(a.expires_at)) &&
    !!cfg?.Backend &&
    !!cfg.Provider &&
    !!cfg.Region &&
    !!cfg.InstanceType &&
    Number.isFinite(cfg.Rate) &&
    cfg.Rate > 0 &&
    Number.isInteger(cfg.Workers) &&
    cfg.Workers > 0 &&
    Array.isArray(cfg.Ports) &&
    cfg.Ports.length > 0 &&
    Array.isArray(cfg.RequestedTargets) &&
    cfg.RequestedTargets.length > 0 &&
    Array.isArray(cfg.Targets) &&
    cfg.Targets.length > 0 &&
    Number.isInteger(a.effective_workers) &&
    a.effective_workers > 0 &&
    Number.isInteger(a.shard_count) &&
    a.shard_count > 0 &&
    Number.isFinite(a.workload?.Addresses) &&
    a.workload.Addresses > 0 &&
    Number.isFinite(a.workload.Probes) &&
    a.workload.Probes > 0
  )
}

function storageKey(planId: string) {
  return `masscanr:start:${planId}`
}

function readAttempt(planId: string): StartAttempt | null {
  try {
    const raw = sessionStorage.getItem(storageKey(planId))
    if (!raw) return null
    const value: unknown = JSON.parse(raw)
    if (
      value &&
      typeof value === 'object' &&
      'preflightId' in value &&
      typeof value.preflightId === 'string' &&
      value.preflightId &&
      'key' in value &&
      typeof value.key === 'string' &&
      value.key
    )
      return { preflightId: value.preflightId, key: value.key }
  } catch {
    // An unavailable or malformed tab store cannot be used for recovery.
  }
  return null
}

function saveAttempt(planId: string, attempt: StartAttempt): boolean {
  try {
    sessionStorage.setItem(storageKey(planId), JSON.stringify(attempt))
    return true
  } catch {
    return false
  }
}

function clearAttempt(planId: string) {
  try {
    sessionStorage.removeItem(storageKey(planId))
  } catch {
    // The request outcome is already known; in-memory state is cleared below.
  }
}

function Detail({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</dt>
      <dd className="mt-1 break-words text-sm font-medium">{children}</dd>
    </div>
  )
}

function PreflightReview({ preflight }: { preflight: ScanPreflight }) {
  const a = preflight.attributes
  const cfg = a.config
  return (
    <div className="space-y-5">
      <p className="text-sm text-muted-foreground">
        Frozen preflight {preflight.id} · Expires {formatTime(a.expires_at)}. Review the resolved
        scope and worker settings before starting.
      </p>
      <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2 xl:grid-cols-3">
        <Detail label="Target revision">
          {a.target_id ?? 'Unknown'} · {a.target_revision ?? 'Unknown'}
        </Detail>
        <Detail label="ScanPlan revision">{a.scan_plan_revision ?? 'Unknown'}</Detail>
        <Detail label="Ports">{cfg.Ports?.join(', ') || 'None'}</Detail>
        <Detail label="Backend">{cfg.Backend}</Detail>
        <Detail label="Provider">{cfg.Provider}</Detail>
        <Detail label="Region">{cfg.Region}</Detail>
        <Detail label="Instance type">{cfg.InstanceType}</Detail>
        <Detail label="Requested workers">{cfg.Workers}</Detail>
        <Detail label="Effective workers">{a.effective_workers}</Detail>
        <Detail label="Shards">{a.shard_count}</Detail>
        <Detail label="Addresses">{a.workload.Addresses.toLocaleString()}</Detail>
        <Detail label="Probe count">{a.workload.Probes.toLocaleString()}</Detail>
        <Detail label="Rate per worker">{cfg.Rate.toLocaleString()} probes/second</Detail>
        <Detail label="Server confirmation">
          {a.requires_confirmation ? 'Required' : 'Not required by server'}
        </Detail>
      </dl>
      <div>
        <h3 className="text-sm font-semibold">Requested Target selectors</h3>
        <p className="text-xs text-muted-foreground">
          These inputs are shown for provenance. They are not the final scan scope.
        </p>
        <ul className="mt-2 space-y-2">
          {(cfg.RequestedTargets ?? []).map((target, index) => (
            <li key={`${target.input}-${index}`} className="rounded-md border p-3 text-sm">
              <code className="break-all">{target.input}</code>
              {target.target !== target.input && (
                <span className="ml-2 text-muted-foreground">Normalized: {target.target}</span>
              )}
              {target.resolution && (
                <p className="mt-1 text-xs text-muted-foreground">
                  AS source: {target.resolution.source} · Snapshot{' '}
                  {formatTime(target.resolution.snapshot_at)} · Fetched{' '}
                  {formatTime(target.resolution.fetched_at)}
                </p>
              )}
              <p className="mt-1 break-all text-xs">
                Resolved: {target.prefixes?.join(', ') || 'None'}
              </p>
            </li>
          ))}
        </ul>
      </div>
      <div>
        <h3 className="text-sm font-semibold">Effective IPv4 scan scope</h3>
        <p className="text-xs text-muted-foreground">
          This frozen, resolved scope is what the server will scan.
        </p>
        <div className="mt-2 max-h-48 overflow-y-auto rounded-md border p-3 text-sm">
          <code className="break-all">{cfg.Targets?.join(', ') || 'None'}</code>
        </div>
      </div>
    </div>
  )
}

export function ScanStart({ plan }: { plan: ScanPlan }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [preflight, setPreflight] = useState<ScanPreflight | null>(null)
  const [attempt, setAttempt] = useState<StartAttempt | null>(() => readAttempt(plan.id))
  const [loadingPreflight, setLoadingPreflight] = useState(false)
  const [starting, setStarting] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [message, setMessage] = useState<string | null>(null)

  async function requestPreflight() {
    if (attempt || loadingPreflight || starting) return
    setLoadingPreflight(true)
    setMessage(null)
    setPreflight(null)
    try {
      const response = await api.preflightPlan(plan.id)
      if (!reviewablePreflight(response.data, plan.id))
        throw new Error(
          'Preflight response lacks reviewable scope or revisions. The scan was not started.',
        )
      setPreflight(response.data)
    } catch (error) {
      setMessage(
        error instanceof ApiError && error.status === 409
          ? 'The ScanPlan or Target changed or was archived. Reload the ScanPlan before requesting another preflight.'
          : error instanceof Error
            ? error.message
            : 'Could not create a preflight.',
      )
    } finally {
      setLoadingPreflight(false)
    }
  }

  async function submitStart(next: StartAttempt) {
    if (starting) return
    setStarting(true)
    setConfirmOpen(false)
    setMessage(null)
    try {
      const response = await api.startPlan(plan.id, next.preflightId, next.key)
      clearAttempt(plan.id)
      setAttempt(null)
      void queryClient.invalidateQueries({ queryKey: ['runs'] })
      navigate(`/runs/${encodeURIComponent(response.data.id)}`)
    } catch (error) {
      if (error instanceof ApiError && [400, 403, 404, 409].includes(error.status)) {
        clearAttempt(plan.id)
        setAttempt(null)
        setPreflight(null)
        setMessage(
          error.status === 409
            ? 'Start was rejected. The preflight may be stale or expired, the Target or ScanPlan may be archived, or another run may be active. Check Runs, then request a fresh preflight.'
            : `Start was rejected: ${error.message}. Check the ScanPlan before requesting a fresh preflight.`,
        )
      } else {
        setMessage(
          error instanceof ApiError &&
            error.status === 503 &&
            error.message.includes('required to start scans')
            ? `Provider credentials are missing: ${error.message}. After they are configured, retry this same attempt while its preflight is valid.`
            : `Start outcome is uncertain: ${error instanceof Error ? error.message : 'request failed'}. Check Runs, then retry this same attempt with its existing preflight ID and idempotency key. Do not request a new preflight until the outcome is known.`,
        )
      }
    } finally {
      setStarting(false)
    }
  }

  function confirmStart() {
    if (!preflight || attempt || starting) return
    if (Date.now() >= Date.parse(preflight.attributes.expires_at)) {
      setConfirmOpen(false)
      setPreflight(null)
      setMessage('This preflight expired. Request a fresh preflight and review its scope again.')
      return
    }
    let key: string
    try {
      key = newIdempotencyKey()
    } catch {
      setConfirmOpen(false)
      setMessage('Secure random values are unavailable. The scan was not started.')
      return
    }
    const next = { preflightId: preflight.id, key }
    if (!saveAttempt(plan.id, next)) {
      setConfirmOpen(false)
      setMessage(
        'Browser tab storage is unavailable. The scan was not started; enable it and retry.',
      )
      return
    }
    setAttempt(next)
    void submitStart(next)
  }

  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Start a scan</CardTitle>
      </CardHeader>
      <CardContent className="space-y-5">
        <p className="text-sm text-muted-foreground">
          A scan may create billable workers and send network traffic. Request a fresh preflight to
          review its frozen scope and settings.
        </p>
        {attempt ? (
          <div role="status" className="space-y-3 rounded-md border p-4 text-sm">
            <p className="font-medium">A start attempt needs a confirmed outcome.</p>
            <p>
              Check{' '}
              <Link to="/runs" className="text-primary underline">
                Runs
              </Link>{' '}
              first. Retry sends the same preflight ID and idempotency key; it does not request a
              new preflight.
            </p>
            <Button disabled={starting} onClick={() => void submitStart(attempt)}>
              {starting ? 'Checking start…' : 'Retry same start attempt'}
            </Button>
          </div>
        ) : (
          <Button disabled={loadingPreflight} onClick={() => void requestPreflight()}>
            {loadingPreflight ? 'Preparing preflight…' : 'Request fresh preflight'}
          </Button>
        )}
        {message && (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"
          >
            {message}
          </p>
        )}
        {preflight && !attempt && (
          <div className="space-y-5">
            <PreflightReview preflight={preflight} />
            <Button onClick={() => setConfirmOpen(true)}>Review and start scan</Button>
          </div>
        )}
        <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Confirm scan start</DialogTitle>
              <DialogDescription>
                This starts a scan against the effective IPv4 scope shown above and may create
                billable workers. The server{' '}
                {preflight?.attributes.requires_confirmation ? 'requires' : 'does not require'}{' '}
                confirmation; this dashboard always asks for it.
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
              <Button disabled={starting} onClick={confirmStart}>
                Confirm and start scan
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  )
}
