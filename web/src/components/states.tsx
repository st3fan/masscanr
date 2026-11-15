import type { ReactNode } from 'react'
import { AlertCircle, Inbox, RefreshCcw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

export function LoadingState() {
  return (
    <div aria-label="Loading" role="status" className="space-y-3">
      <Skeleton className="h-16 w-full" />
      <Skeleton className="h-16 w-full" />
      <Skeleton className="h-16 w-3/4" />
    </div>
  )
}
export function EmptyState({
  title,
  description,
  action,
}: {
  title: string
  description: string
  action?: ReactNode
}) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center py-16 text-center">
        <Inbox className="mb-4 size-9 text-muted-foreground" aria-hidden="true" />
        <h2 className="font-semibold">{title}</h2>
        <p className="mt-1 max-w-md text-sm text-muted-foreground">{description}</p>
        {action && <div className="mt-5">{action}</div>}
      </CardContent>
    </Card>
  )
}
export function ErrorState({ error, retry }: { error: Error; retry?: () => void }) {
  return (
    <Card role="alert">
      <CardContent className="flex flex-col items-start gap-3 py-8">
        <AlertCircle className="size-6 text-destructive" aria-hidden="true" />
        <div>
          <h2 className="font-semibold">Unable to load data</h2>
          <p className="text-sm text-muted-foreground">{error.message}</p>
        </div>
        {retry && (
          <Button variant="outline" onClick={retry}>
            <RefreshCcw />
            Retry
          </Button>
        )}
      </CardContent>
    </Card>
  )
}
