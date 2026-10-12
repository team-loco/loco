# Loco web

The Loco dashboard: React 19 with the React Compiler, Vite, Tailwind 4, shadcn on Base UI, and
ConnectRPC clients generated into `../gen/ts`.

## Commands

Run them through mise from the repo root, which pins bun and every other tool:

| Task                | What it does                                        |
| ------------------- | --------------------------------------------------- |
| `mise run ui`       | Vite dev server                                     |
| `mise run lint:web` | oxlint, then type-aware eslint, then `bun check`      |
| `mise run test:web` | `bun test`                                          |

`bun run build` type-checks and writes the production bundle to `dist/`.

## Configuration

`VITE_API_URL` is the API base URL and `VITE_APP_ENV=PRODUCTION` switches the transport to the
binary protobuf format. `.env.example` lists both with their local values.

## Checks

- `tsconfig.base.json` holds the compiler options and path aliases every project shares;
  `tsconfig.app.json`, `tsconfig.test.json` and `tsconfig.node.json` extend it.
- `.oxlintrc.json` runs the rules that need no type information, including accessibility.
- `eslint.config.js` runs only what oxlint cannot: the type-aware typescript-eslint rules, the
  React Compiler rules from `eslint-plugin-react-hooks` and `@eslint-react`.

Every warning fails. Frontend conventions (design wrappers, theme tokens, effects, errors) are
in the repo's `AGENTS.md`.
