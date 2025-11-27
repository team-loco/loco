import { useState } from 'react'
import type { User } from '@/gen/user/v1/user_pb'
import { useAuth } from '@/auth/AuthProvider'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

interface NavbarProps {
  user: User | null
}

export function Navbar({ user }: NavbarProps) {
  const [showUserMenu, setShowUserMenu] = useState(false)
  const { logout } = useAuth()

  const handleLogout = () => {
    logout()
    setShowUserMenu(false)
    // Optionally redirect or reload
    window.location.href = '/'
  }

  return (
    <nav className="border-b border-border bg-secondary-background shadow-neo">
      <div className="max-w-7xl mx-auto px-6 py-4 flex items-center justify-between">
        {/* Logo */}
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 bg-main rounded-neo flex items-center justify-center text-white font-heading text-sm">
            L
          </div>
          <h1 className="text-xl font-heading">Loco</h1>
        </div>

        {/* Nav Links */}
        <div className="hidden md:flex items-center gap-8">
          <a
            href="/"
            className="text-foreground hover:text-main transition-colors font-base"
          >
            Home
          </a>
          <a
            href="/apps"
            className="text-foreground hover:text-main transition-colors font-base"
          >
            Apps
          </a>
          <a
            href="https://docs.loco.dev"
            target="_blank"
            rel="noopener noreferrer"
            className="text-foreground hover:text-main transition-colors font-base"
          >
            Docs
          </a>
          <a
            href="/observability"
            className="text-foreground hover:text-main transition-colors font-base"
          >
            Obs
          </a>
        </div>

        {/* User Profile */}
        <div className="relative">
          {user ? (
            <DropdownMenu open={showUserMenu} onOpenChange={setShowUserMenu}>
              <DropdownMenuTrigger asChild>
                <Button variant="neutral" className="flex items-center gap-2">
                  <div className="w-5 h-5 bg-main rounded-neo text-white flex items-center justify-center text-xs font-heading">
                    {user.name.charAt(0).toUpperCase()}
                  </div>
                  <span className="text-sm">{user.name}</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem asChild>
                  <a href="/profile">Profile</a>
                </DropdownMenuItem>
                <DropdownMenuItem asChild>
                  <a href="/settings">Settings</a>
                </DropdownMenuItem>
                <DropdownMenuItem onClick={handleLogout}>Logout</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <Button>Sign In</Button>
          )}
        </div>
      </div>
    </nav>
  )
}
