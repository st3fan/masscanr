import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '@/api/client'

export function TargetLink({ id }: { id: string }) {
  const target = useQuery({
    queryKey: ['target', id],
    queryFn: ({ signal }) => api.target(id, signal),
    enabled: !!id,
  })
  return (
    <Link className="break-all text-primary hover:underline" to={`/targets/${id}`}>
      {target.data?.data.attributes.name ?? id}
    </Link>
  )
}
