import { Badge } from '@/components/ui/badge'

interface StatusBadgeProps {
  status: string
}

const statusConfig: Record<
  string,
  { className: string; dot: string }
> = {
  running: {
    className: 'bg-chart-4 text-black',
    dot: 'bg-chart-4',
  },
  deploying: {
    className: 'bg-chart-1 text-black',
    dot: 'bg-chart-1',
  },
  stopped: {
    className: 'bg-muted text-foreground',
    dot: 'bg-muted-foreground',
  },
  failed: {
    className: 'bg-destructive text-white',
    dot: 'bg-destructive',
  },
  pending: {
    className: 'bg-chart-2 text-white',
    dot: 'bg-chart-2',
  },
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const normalizedStatus = status.toLowerCase()
  const config = statusConfig[normalizedStatus] || statusConfig.pending

  return (
    <Badge className={config.className}>
      <span className={`w-2 h-2 rounded-full ${config.dot}`}></span>
      {status.charAt(0).toUpperCase() + status.slice(1)}
    </Badge>
  )
}
