import { useEffect, useState, type ReactNode } from 'react'
import { useTheme } from 'next-themes'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  Activity,
  Blocks,
  CircleHelp,
  LayoutDashboard,
  ListFilter,
  Menu,
  Moon,
  Radar,
  Sun,
  Target,
  X,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const navigation = [
  { to: '/', label: 'Overview', icon: LayoutDashboard, end: true },
  { to: '/targets', label: 'Targets', icon: Target },
  { to: '/scan-plans', label: 'Scan plans', icon: Blocks },
  { to: '/runs', label: 'Scan runs', icon: Activity },
  { to: '/results', label: 'Results', icon: ListFilter },
]

export function AppLayout() {
  const { resolvedTheme, setTheme } = useTheme()
  const dark = resolvedTheme === 'dark'
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()
  useEffect(() => setMobileOpen(false), [location.pathname])
  return (
    <div className="min-h-screen bg-background text-foreground lg:grid lg:grid-cols-[250px_minmax(0,1fr)]">
      {mobileOpen && (
        <button
          aria-label="Close navigation"
          className="fixed inset-0 z-30 bg-black/45 lg:hidden"
          onClick={() => setMobileOpen(false)}
        />
      )}
      <aside
        className={cn(
          'fixed inset-y-0 left-0 z-40 flex w-[250px] flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground transition-transform lg:sticky lg:top-0 lg:h-screen lg:translate-x-0',
          mobileOpen ? 'translate-x-0' : '-translate-x-full',
        )}
      >
        <div className="flex h-16 items-center gap-3 border-b border-sidebar-border px-5">
          <span className="flex size-9 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Radar className="size-5" aria-hidden="true" />
          </span>
          <div>
            <div className="font-semibold tracking-tight">masscanr</div>
            <div className="text-xs text-muted-foreground">Exposure inventory</div>
          </div>
          <Button
            className="ml-auto lg:hidden"
            variant="ghost"
            size="icon"
            aria-label="Close menu"
            onClick={() => setMobileOpen(false)}
          >
            <X />
          </Button>
        </div>
        <nav aria-label="Primary" className="flex-1 space-y-1 p-3">
          <div className="px-3 pb-2 pt-3 text-[11px] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
            Workspace
          </div>
          {navigation.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 rounded-md px-3 py-2.5 text-sm font-medium outline-none transition-colors hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-ring',
                  isActive && 'bg-sidebar-accent text-sidebar-accent-foreground',
                )
              }
            >
              <Icon className="size-4" aria-hidden="true" />
              {label}
            </NavLink>
          ))}
        </nav>
        <div className="border-t border-sidebar-border p-3">
          <NavLink
            to="/patterns"
            className="flex items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground hover:bg-sidebar-accent"
          >
            <CircleHelp className="size-4" />
            Component reference
          </NavLink>
        </div>
      </aside>
      <div className="min-w-0">
        <header className="sticky top-0 z-20 flex h-16 items-center gap-3 border-b bg-background/95 px-4 backdrop-blur md:px-8">
          <Button
            variant="ghost"
            size="icon"
            className="lg:hidden"
            aria-label="Open menu"
            onClick={() => setMobileOpen(true)}
          >
            <Menu />
          </Button>
          <span className="text-sm text-muted-foreground">
            Inventory /{' '}
            <span className="font-medium text-foreground">
              {navigation.find((item) => item.to === location.pathname)?.label ?? 'Details'}
            </span>
          </span>
          <div className="ml-auto flex items-center gap-2">
            <span className="hidden rounded-full border px-2.5 py-1 text-xs text-muted-foreground sm:inline">
              Private listener
            </span>
            <Button
              variant="ghost"
              size="icon"
              aria-label={dark ? 'Use light theme' : 'Use dark theme'}
              onClick={() => setTheme(dark ? 'light' : 'dark')}
            >
              {dark ? <Sun /> : <Moon />}
            </Button>
          </div>
        </header>
        <main id="main-content" className="mx-auto max-w-[1500px] px-4 py-7 md:px-8 md:py-9">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function PageHeader({
  eyebrow,
  title,
  description,
  action,
}: {
  eyebrow?: string
  title: string
  description?: string
  action?: ReactNode
}) {
  return (
    <div className="mb-8 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div>
        {eyebrow && (
          <p className="mb-2 text-xs font-semibold uppercase tracking-[0.16em] text-primary">
            {eyebrow}
          </p>
        )}
        <h1 className="text-3xl font-semibold tracking-tight">{title}</h1>
        {description && (
          <p className="mt-2 max-w-2xl text-sm text-muted-foreground">{description}</p>
        )}
      </div>
      {action}
    </div>
  )
}
