import { useState } from 'react'
import { useAuth } from '@/auth/AuthProvider'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

export function Login() {
  const [token, setToken] = useState('')
  const [error, setError] = useState<string | null>(null)
  const { login } = useAuth()

  const handleLogin = (e: React.FormEvent) => {
    e.preventDefault()
    if (!token.trim()) {
      setError('Please enter a token')
      return
    }
    login(token)
    // Redirect will be handled by the app
    window.location.href = '/'
  }

  return (
    <div className="min-h-screen bg-background flex items-center justify-center px-6">
      <Card className="max-w-md w-full">
        <CardContent className="p-8">
          <div className="mb-6 text-center">
            <div className="w-12 h-12 bg-main rounded-neo flex items-center justify-center text-white font-heading text-lg mx-auto mb-4">
              L
            </div>
            <h1 className="text-2xl font-heading text-foreground">Loco</h1>
            <p className="text-sm text-foreground opacity-60 mt-2">Deploy with confidence</p>
          </div>

          <form onSubmit={handleLogin} className="space-y-4">
            <div className="space-y-2">
              <label className="text-sm font-base text-foreground">Bearer Token</label>
              <input
                type="password"
                value={token}
                onChange={(e) => {
                  setToken(e.target.value)
                  setError(null)
                }}
                placeholder="paste your bearer token"
                className="w-full px-3 py-2 border-2 border-border rounded-neo bg-secondary-background text-foreground focus:outline-none focus:ring-2 focus:ring-main text-sm"
              />
            </div>

            {error && <p className="text-xs text-destructive">{error}</p>}

            <Button type="submit" className="w-full">
              Login
            </Button>
          </form>

          <p className="text-xs text-foreground opacity-50 text-center mt-4">
            Get your token from your OAuth provider or backend admin panel
          </p>
        </CardContent>
      </Card>
    </div>
  )
}
