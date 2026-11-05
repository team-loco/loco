# Frontend Guidelines

Paths and commands below are relative to `web/`.

- **Components**: `src/components/ui/` is stock vendored shadcn and is never edited. All Loco
  styling goes in `src/components/design/<Name>.tsx` wrappers layered on with `cn()`, and pages
  import from `design/`.
- **Colours**: use the theme tokens (`bg-bg2`, `text-fg3`, `border-line`, the `ok/warn/bad`
  pairs), never raw hex or stock Tailwind palette colours. Check every change in light and dark.
- **Cursors**: the base CSS sets the pointer and disabled cursors; do not add `cursor-pointer`
  per element.
- **Effects**: no `useEffect` unless it syncs with an external system. Derive values during
  render and do work in event handlers; for timers or observers use `useSyncExternalStore` or a
  ref callback that returns a cleanup. Follow
  https://github.com/vercel-labs/agent-skills/blob/main/skills/react-best-practices/AGENTS.md.
- **Production build**: the dev server does not catch bundling bugs. Any change to
  `build.ts` or dependencies needs `bun run build` and `bun run preview`, loaded in a browser,
  before the PR.
- **Errors**: user-facing error text goes through `src/lib/error-handler.ts`:
  `getErrorMessage(error, fallback)` for a string, `toastConnectError(error, fallback)` for a
  toast. Both handle `ConnectError`, `Error` and unknown values.
- **PR screenshots**: UI changes need before/after screenshots in light and dark mode, plus
  a narrow viewport for layout changes. Use `ui-evidence` and upload with `gh pr edit --attach`.
