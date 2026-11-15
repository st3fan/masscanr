import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { EmptyState, ErrorState, LoadingState } from '@/components/states'
import { PageHeader } from '@/components/layout'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { toast } from 'sonner'

export function PatternsPage() {
  return (
    <>
      <PageHeader
        eyebrow="Design reference"
        title="Component patterns"
        description="Shared visual states for future dashboard features."
      />
      <div className="grid gap-5 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Controls and status</CardTitle>
            <CardDescription>Use semantic variants and consistent spacing.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            <div className="flex flex-wrap gap-2">
              <Button>Primary action</Button>
              <Button variant="outline">Secondary</Button>
              <Button variant="ghost">Tertiary</Button>
              <Dialog>
                <DialogTrigger render={<Button variant="outline" />}>Dialog</DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Confirmation pattern</DialogTitle>
                    <DialogDescription>
                      Use this shared dialog for future actions that need confirmation.
                    </DialogDescription>
                  </DialogHeader>
                </DialogContent>
              </Dialog>
              <Button variant="ghost" onClick={() => toast.success('Notification pattern')}>
                Notification
              </Button>
            </div>
            <div className="flex gap-2">
              <Badge>Active</Badge>
              <Badge variant="secondary">Complete</Badge>
              <Badge variant="destructive">Failed</Badge>
            </div>
            <Input aria-label="Example filter" placeholder="Filter records" />
            <Progress value={62} aria-label="Example progress" />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Loading</CardTitle>
            <CardDescription>Keep the page shape while data loads.</CardDescription>
          </CardHeader>
          <CardContent>
            <LoadingState />
          </CardContent>
        </Card>
        <EmptyState
          title="No records yet"
          description="New records will appear here when the server has them."
        />
        <ErrorState error={new Error('The server could not be reached.')} />
      </div>
    </>
  )
}
