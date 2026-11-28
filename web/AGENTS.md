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
- **Simple conditions**: a condition reads at a glance. More than two clauses means the
  code is missing a name: give the question it answers one variable (`orgPending`) built from
  query state (`isPending`, `isFetching`, `data`), not from restated permission checks. When a
  condition needs a comment to explain it, change the data flow, not the comment.
- **No per-user special cases**: a fix that branches on the kind of user (system admin,
  workspace grant only) to hide a race or a flash is wrong. Make the page wait on the server's
  answer the same way for everyone.
- **Loading**: once the shell has rendered it stays up. Waits inside a page show `PageLoading`
  (`src/components/design/Page.tsx`); the full-screen `AppLoading` covers only the first
  sign-in check.
- **Review**: a web review reads the diff for complexity before correctness. A condition with
  more than two clauses, a new branch per kind of user, or a new full-screen loading state is a
  blocking finding. Check what merged the same day in the files the PR touches, so it does not
  undo it.
- **Production build**: the dev server does not catch bundling bugs. Any change to
  `build.ts` or dependencies needs `bun run build` and `bun run preview`, loaded in a browser,
  before the PR.
- **Errors**: user-facing error text goes through `src/lib/error-handler.tsx`:
  `getErrorMessage(error, fallback)` for a string, `toastConnectError(error, fallback)` for a
  toast. Both handle `ConnectError`, `Error` and unknown values.
- **UX-only preview**: use `mise run ui:staging` from the repository root. It serves local UI
  changes against real staging data without starting the local platform. Use the saved staging
  CLI login; browser sign-in, sign-out and web-session refresh need an isolated fixture.
- **PR screenshots**: UI changes need before/after screenshots in light and dark mode, plus
  a narrow viewport for layout changes. Prefer local captures against `mise run ui:staging`
  for UX-only changes; inspect every image, label real staging data and attach with
  `gh pr edit --attach`. Keep authentication state and staging browser traces private.
  If local capture or suitable data is unavailable, use `mise run ci:evidence` with an isolated
  fixture. Deterministic browser suites continue to use isolated fixtures in CI.
