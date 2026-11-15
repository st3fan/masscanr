import type { ReactNode } from 'react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

export function StatusBadge({ phase, active }: { phase: string; active?: boolean }) {
  const tone =
    phase === 'failed' || phase === 'cleanup_incomplete'
      ? 'destructive'
      : active
        ? 'default'
        : 'secondary'
  return (
    <Badge variant={tone} className="capitalize">
      {phase.replaceAll('_', ' ') || 'Unknown'}
    </Badge>
  )
}
export function MetricCard({
  label,
  value,
  detail,
  icon,
}: {
  label: string
  value: string | number
  detail?: string
  icon?: ReactNode
}) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-sm font-medium text-muted-foreground">{label}</CardTitle>
        {icon && <span className="text-muted-foreground">{icon}</span>}
      </CardHeader>
      <CardContent>
        <div className="text-3xl font-semibold tracking-tight">{value}</div>
        {detail && <p className="mt-2 text-xs text-muted-foreground">{detail}</p>}
      </CardContent>
    </Card>
  )
}
export function formatTime(value?: string) {
  if (!value) return 'Unknown'
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? 'Unknown' : date.toLocaleString()
}
