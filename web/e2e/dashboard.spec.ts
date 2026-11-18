import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

const target = {
  type: 'targets',
  id: 't-1',
  attributes: {
    name: 'External network',
    entries: [
      { id: 'e-1', position: 0, kind: 'cidr', selector: '192.0.2.0/24', input: '192.0.2.0/24' },
    ],
    revision: 1,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  },
}
const plan = {
  type: 'scan-plans',
  id: 'p-1',
  attributes: {
    name: 'Routine TCP',
    target_id: 't-1',
    revision: 1,
    archived_at: null as string | null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    settings: {
      ports: '80,443',
      backend: 'masscan',
      provider: 'vultr',
      region: 'ewr',
      instance_type: 'vc2-1c-1gb',
      workers: 2,
      rate: 1000,
      timeout: '24h',
      cleanup_timeout: '5m',
    },
  },
}
const run = {
  type: 'runs',
  id: 'r-1',
  attributes: {
    active: false,
    settled: true,
    stop_requested: false,
    phase: 'completed',
    target_id: 't-1',
    scan_plan_id: 'p-1',
    created_at: '2026-01-02T00:00:00Z',
    artifact: '',
    snapshot_version: 2,
    preflight: { id: 'pf-1', config: {} },
    summary: {
      Found: 1,
      CleanupOutcome: 'complete',
      ScanOutcome: 'complete',
      ReconciliationUnresolved: false,
    },
    worker_snapshots: {},
  },
}
const result = {
  type: 'scan-results',
  id: 'res-1',
  attributes: {
    run_id: 'r-1',
    target_id: 't-1',
    ip: '192.0.2.1',
    port: 443,
    protocol: 'tcp',
    first_seen: 1700000000,
    last_seen: 1700000000,
    observations: 1,
    coverage: 'complete',
  },
}

test.beforeEach(async ({ page }) => {
  let targetState: typeof target & {
    attributes: typeof target.attributes & { archived_at?: string }
  } = structuredClone(target)
  let planState = structuredClone(plan)
  await page.route('**/healthz', (route) => route.fulfill({ json: { status: 'ready' } }))
  await page.route('**/v1/**', (route) => {
    const requestURL = new URL(route.request().url())
    const path = requestURL.pathname
    const method = route.request().method()
    if (path === '/v1/targets' && method === 'POST') {
      const body = route.request().postDataJSON() as {
        data: { attributes: { name: string; entries: string[] } }
      }
      targetState = {
        ...targetState,
        attributes: {
          ...targetState.attributes,
          name: body.data.attributes.name,
          entries: body.data.attributes.entries.map((input, position) => ({
            id: `e-${position + 1}`,
            position,
            kind: 'ip',
            selector: `${input}/32`,
            input,
          })),
        },
      }
      return route.fulfill({
        status: 201,
        contentType: 'application/vnd.api+json',
        body: JSON.stringify({ data: targetState }),
      })
    }
    if (path === '/v1/targets/t-1' && method === 'PATCH') {
      const body = route.request().postDataJSON() as {
        data: { attributes: { revision: number; name?: string; archived?: boolean } }
      }
      targetState = {
        ...targetState,
        attributes: {
          ...targetState.attributes,
          name: body.data.attributes.name ?? targetState.attributes.name,
          revision: targetState.attributes.revision + 1,
          ...(body.data.attributes.archived ? { archived_at: '2026-01-03T00:00:00Z' } : {}),
        },
      }
      return route.fulfill({
        contentType: 'application/vnd.api+json',
        body: JSON.stringify({ data: targetState }),
      })
    }
    if (path.startsWith('/v1/targets/by-name/')) {
      return route.fulfill({
        contentType: 'application/vnd.api+json',
        body: JSON.stringify({ data: targetState }),
      })
    }
    if (path === '/v1/catalog/regions') {
      const provider = requestURL.searchParams.get('provider')
      const region =
        provider === 'digitalocean'
          ? { type: 'regions', id: 'tor1', attributes: { name: 'Toronto' } }
          : { type: 'regions', id: 'ewr', attributes: { name: 'New Jersey' } }
      return route.fulfill({
        contentType: 'application/vnd.api+json',
        body: JSON.stringify({ data: [region] }),
      })
    }
    if (path === '/v1/catalog/instance-types') {
      const id =
        requestURL.searchParams.get('provider') === 'digitalocean' ? 's-1vcpu-1gb' : 'vc2-1c-1gb'
      return route.fulfill({
        contentType: 'application/vnd.api+json',
        body: JSON.stringify({
          data: [{ type: 'instance-types', id, attributes: { vcpus: 1, memory_mb: 1024 } }],
        }),
      })
    }
    if (path === '/v1/scan-plans' && method === 'POST') {
      const body = route.request().postDataJSON() as {
        data: { attributes: { name: string; settings: typeof plan.attributes.settings } }
      }
      planState = {
        ...planState,
        attributes: {
          ...planState.attributes,
          name: body.data.attributes.name,
          settings: body.data.attributes.settings,
        },
      }
      return route.fulfill({
        status: 201,
        contentType: 'application/vnd.api+json',
        body: JSON.stringify({ data: planState }),
      })
    }
    if (path === '/v1/scan-plans/p-1' && method === 'PATCH') {
      const body = route.request().postDataJSON() as {
        data: {
          attributes: {
            name?: string
            settings?: Partial<typeof plan.attributes.settings>
            archived?: boolean
          }
        }
      }
      planState = {
        ...planState,
        attributes: {
          ...planState.attributes,
          name: body.data.attributes.name ?? planState.attributes.name,
          settings: { ...planState.attributes.settings, ...body.data.attributes.settings },
          revision: planState.attributes.revision + 1,
          archived_at: body.data.attributes.archived
            ? '2026-01-03T00:00:00Z'
            : planState.attributes.archived_at,
        },
      }
      return route.fulfill({
        contentType: 'application/vnd.api+json',
        body: JSON.stringify({ data: planState }),
      })
    }
    const data =
      path === '/v1/targets'
        ? [targetState]
        : path === '/v1/targets/t-1'
          ? targetState
          : path === '/v1/scan-plans'
            ? [planState]
            : path === '/v1/scan-plans/p-1'
              ? planState
              : path === '/v1/runs'
                ? requestURL.searchParams.get('active') === 'true'
                  ? []
                  : [run]
                : path === '/v1/runs/r-1'
                  ? run
                  : path === '/v1/scan-results'
                    ? [result]
                    : []
    return route.fulfill({
      contentType: 'application/vnd.api+json',
      body: JSON.stringify({
        data,
        meta:
          path === '/v1/scan-results'
            ? { coverage: { state: 'complete', run_phase: 'completed', journal_checkpoint: 1 } }
            : {},
      }),
    })
  })
})

test('navigates inventory pages and captures the design', async ({ page }, testInfo) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('overview-light.png'), fullPage: true })
  if (testInfo.project.name === 'mobile')
    await page.getByRole('button', { name: 'Open menu' }).click()
  await page
    .getByRole('navigation', { name: 'Primary' })
    .getByRole('link', { name: 'Targets' })
    .click()
  await expect(page.getByRole('link', { name: 'External network' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('targets-light.png'), fullPage: true })
  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await page.screenshot({ path: testInfo.outputPath('targets-dark.png'), fullPage: true })
  await page.goto('/scan-plans')
  await expect(page.getByRole('link', { name: 'Routine TCP' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('plans-dark.png'), fullPage: true })
  await page.goto('/runs')
  await expect(page.getByRole('heading', { name: 'Scan runs' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('runs-dark.png'), fullPage: true })
  const report = await new AxeBuilder({ page }).analyze()
  expect(report.violations.filter((v) => ['critical', 'serious'].includes(v.impact ?? ''))).toEqual(
    [],
  )
})

test('creates, edits, and archives a Target with accessible confirmation', async ({
  page,
}, testInfo) => {
  await page.goto('/targets/new')
  await expect(page.getByRole('heading', { name: 'Create target' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('target-create-light.png'), fullPage: true })
  await page.getByRole('textbox', { name: 'Name' }).fill('New authorized network')
  await page.getByRole('textbox', { name: 'Selector 1' }).fill('192.0.2.1')
  await page.getByRole('button', { name: 'Create target' }).click()
  await expect(page.getByRole('heading', { name: 'New authorized network' })).toBeVisible()
  await page.getByRole('link', { name: 'Edit target' }).click()
  await page.getByRole('textbox', { name: 'Name' }).fill('Renamed network')
  await page.getByRole('button', { name: 'Save target' }).click()
  await expect(page.getByRole('heading', { name: 'Renamed network' })).toBeVisible()
  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await page.getByRole('button', { name: 'Archive target' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'Archive this Target?' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('target-archive-dark.png'), fullPage: true })
  const report = await new AxeBuilder({ page }).analyze()
  expect(report.violations.filter((v) => ['critical', 'serious'].includes(v.impact ?? ''))).toEqual(
    [],
  )
  await dialog.getByRole('button', { name: 'Archive target' }).click()
  await expect(page.getByText(/This Target is retained for history/)).toBeVisible()
  await expect(page.getByRole('link', { name: 'Edit target' })).toHaveCount(0)
})

test('creates, edits, and archives a ScanPlan with accessible confirmation', async ({
  page,
}, testInfo) => {
  await page.goto('/scan-plans/new')
  await expect(page.getByRole('heading', { name: 'Create scan plan' })).toBeVisible()
  await page.getByRole('textbox', { name: 'Name' }).fill('Evening TCP')
  await page.getByRole('combobox', { name: 'Active Target name' }).fill('External network')
  await page.getByRole('combobox', { name: 'Region' }).selectOption('ewr')
  await page.getByRole('combobox', { name: 'Instance type' }).selectOption('vc2-1c-1gb')
  await page.screenshot({ path: testInfo.outputPath('scanplan-create-light.png'), fullPage: true })
  const formReport = await new AxeBuilder({ page }).analyze()
  expect(
    formReport.violations.filter((v) => ['critical', 'serious'].includes(v.impact ?? '')),
  ).toEqual([])
  await page.getByRole('button', { name: 'Create scan plan' }).click()
  await expect(page.getByRole('heading', { name: 'Evening TCP' })).toBeVisible()
  await page.getByRole('link', { name: 'Edit scan plan' }).click()
  await page.getByRole('textbox', { name: 'TCP ports' }).fill('443')
  await page.getByRole('button', { name: 'Save scan plan' }).click()
  await expect(page.getByText('443', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await page.getByRole('button', { name: 'Archive scan plan' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'Archive this ScanPlan?' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('scanplan-archive-dark.png'), fullPage: true })
  const dialogReport = await new AxeBuilder({ page }).analyze()
  expect(
    dialogReport.violations.filter((v) => ['critical', 'serious'].includes(v.impact ?? '')),
  ).toEqual([])
  await dialog.getByRole('button', { name: 'Archive scan plan' }).click()
  await expect(page.getByText(/This ScanPlan is read-only/)).toBeVisible()
  await expect(page.getByRole('link', { name: 'Edit scan plan' })).toHaveCount(0)
})

test('shows server result filters and coverage', async ({ page }) => {
  await page.goto('/results?run_id=r-1')
  await expect(page.getByText('192.0.2.1')).toBeVisible()
  await expect(page.getByText(/Run coverage:.*complete/)).toBeVisible()
  await expect(page.getByText(/Missing findings do not imply closed ports/)).toBeVisible()
  await page.getByLabel('IPv4 address').fill('192.0.2.1')
  await page.getByRole('button', { name: 'Apply filters' }).click()
  await expect(page.getByText('192.0.2.1')).toBeVisible()
})
