# Loco web

The Loco dashboard: React 19 with the React Compiler, Bun, Tailwind 4, shadcn on Base UI, and
ConnectRPC clients generated into `../gen/ts`.

## Commands

Run them through mise from the repo root, which pins bun and every other tool:

| Task                | What it does                                     |
| ------------------- | ------------------------------------------------ |
| `mise run ui`       | Bun dev server with hot reload on `:5173`        |
| `mise run lint:web` | oxlint, then type-aware eslint, then `bun check` |
| `mise run test:web` | `bun test`                                       |

`bun run build` type-checks and runs `build.ts`, which writes the production bundle to `dist/`.
`bun run preview` serves `dist/` on `:4173`.

`build.ts` puts React, Base UI, TanStack, recharts, sugar-high, the remaining packages and the
generated `@gen` code in their own chunks, so a deploy that changes only app code leaves them
cached. It finds what the
app imports from each package and generates one entry point per group that re-exports exactly
those names.

## Configuration

`VITE_API_URL` is the API base URL and `VITE_APP_ENV=PRODUCTION` switches the transport to the
binary protobuf format. `.env.example` lists both with their local values. The app imports them
from `loco:env`, a module `env-plugin.ts` generates from the environment at build time.

## Checks

- `tsconfig.base.json` holds the compiler options and path aliases every project shares;
  `tsconfig.app.json`, `tsconfig.test.json` and `tsconfig.node.json` extend it.
- `.oxlintrc.json` runs the rules that need no type information, including accessibility.
- `eslint.config.js` runs only what oxlint cannot: the type-aware typescript-eslint rules, the
  React Compiler rules from `eslint-plugin-react-hooks` and `@eslint-react`.

Every warning fails. Frontend conventions (design wrappers, theme tokens, effects, errors) are
in the repo's `AGENTS.md`.
