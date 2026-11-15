import { useState } from 'react'
export function useCursorPagination() {
  const [history, setHistory] = useState<(string | undefined)[]>([undefined])
  const cursor = history[history.length - 1]
  return {
    cursor,
    hasPrevious: history.length > 1,
    next: (value: string) => setHistory((current) => [...current, value]),
    previous: () => setHistory((current) => (current.length > 1 ? current.slice(0, -1) : current)),
    reset: () => setHistory([undefined]),
  }
}
