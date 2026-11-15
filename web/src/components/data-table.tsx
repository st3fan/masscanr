import { useState } from 'react'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
  type SortingState,
} from '@tanstack/react-table'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { EmptyState } from '@/components/states'

export function DataTable<T>({
  data,
  columns,
  emptyTitle,
  emptyDescription,
  next,
  previous,
  onNext,
  onPrevious,
  onSort,
  sort,
}: {
  data: T[]
  columns: ColumnDef<T>[]
  emptyTitle: string
  emptyDescription: string
  next?: boolean
  previous?: boolean
  onNext?: () => void
  onPrevious?: () => void
  sort?: SortingState
  onSort?: (value: SortingState) => void
}) {
  const [localSort, setLocalSort] = useState<SortingState>([])
  const sorting = sort ?? localSort
  const table = useReactTable({
    data,
    columns,
    state: { sorting },
    onSortingChange: (updater) => {
      const next = typeof updater === 'function' ? updater(sorting) : updater
      if (onSort) onSort(next)
      else setLocalSort(next)
    },
    manualSorting: !!onSort,
    getCoreRowModel: getCoreRowModel(),
  })
  if (!data.length) return <EmptyState title={emptyTitle} description={emptyDescription} />
  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((group) => (
              <TableRow key={group.id}>
                {group.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    aria-sort={
                      header.column.getIsSorted() === 'asc'
                        ? 'ascending'
                        : header.column.getIsSorted() === 'desc'
                          ? 'descending'
                          : 'none'
                    }
                  >
                    {header.isPlaceholder ? null : header.column.getCanSort() ? (
                      <button
                        className="inline-flex items-center gap-1.5 font-medium hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
                        onClick={header.column.getToggleSortingHandler()}
                      >
                        {flexRender(header.column.columnDef.header, header.getContext())}
                        {header.column.getIsSorted() === 'asc' ? (
                          <ArrowUp className="size-3.5" />
                        ) : header.column.getIsSorted() === 'desc' ? (
                          <ArrowDown className="size-3.5" />
                        ) : null}
                      </button>
                    ) : (
                      flexRender(header.column.columnDef.header, header.getContext())
                    )}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.map((row) => (
              <TableRow key={row.id}>
                {row.getVisibleCells().map((cell) => (
                  <TableCell key={cell.id}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {(previous || next) && (
        <div className="flex justify-end gap-2 border-t px-4 py-3">
          <Button variant="outline" size="sm" disabled={!previous} onClick={onPrevious}>
            Previous
          </Button>
          <Button variant="outline" size="sm" disabled={!next} onClick={onNext}>
            Next
          </Button>
        </div>
      )}
    </div>
  )
}
