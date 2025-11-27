import type { App } from '@/gen/app/v1/app_pb'
import { Card, CardContent } from '@/components/ui/card'
import { StatusBadge } from './StatusBadge'

interface AppCardProps {
  app: App
}

export function AppCard({ app }: AppCardProps) {
  return (
    <Card className="cursor-pointer hover:shadow-neo transition-shadow">
      <CardContent className="p-6 space-y-4">
        <div className="flex items-start justify-between">
          <div className="flex-1">
            <h3 className="text-lg font-heading text-foreground">{app.name}</h3>
            <p className="text-sm text-foreground opacity-70 mt-1">{app.namespace}</p>
          </div>
          <StatusBadge status="running" />
        </div>

        <div className="text-xs text-foreground opacity-60 space-y-1">
          <p>Type: {app.type}</p>
          <p>Domain: {app.domain || app.subdomain}</p>
        </div>

        <p className="text-xs text-foreground opacity-50 border-t border-border pt-3 mt-3">
          Created {new Date(app.createdAt).toLocaleDateString()}
        </p>
      </CardContent>
    </Card>
  )
}
