---
name: go-review
description: Review Go code for layout, architecture and quality problems linters cannot catch. Load before writing or reviewing Go in this repo, and when asked for a Go review of a diff, package or PR.
---

# Go review

Linters cover the mechanical rules. This covers the judgment calls, starting with the
mistakes this codebase has made most often. Check a change against every item; the scope is
the change and the code it touches.

## Mistakes we keep making

1. **Log and return.** A function logs an error, then returns it, and its caller logs it
   again. Return the wrapped error; the top of the call (the RPC error interceptor, a
   command's `RunE`, a worker loop) logs it once. Log only where the error stops.
2. **Masking the cause.** Turning every lookup failure into NotFound, or a database error
   into "invalid token", hides outages from clients and operators. Map only the error you
   mean (`sql.ErrNoRows` to NotFound) and wrap the rest.
3. **Interfaces for one implementation.** An interface, a `deps` struct of constructors or a
   function parameter that exists so a test can swap in a fake. Take the concrete type and
   test against the real thing (a test database, `httptest`). Add an interface when a second
   real implementation exists, and declare it where it is consumed.
4. **Copied preambles.** The same opening steps (authorize, resolve host and token, look up
   the workspace, confirm unless `--yes`) pasted into every handler or command, drifting
   apart. The second copy is the time for one shared helper.
5. **Contracts defined twice.** A payload, phase string or resource name declared in both
   the producer and the consumer. One package owns it; a module that cannot import it keeps
   a copy with a comment naming the owner to update together.
6. **Configuration checked late.** Validating config in a reconcile loop or request handler
   means a bad deploy fails on every request instead of at startup. Validate when the config
   struct is read, and panic there.
7. **Long positional parameter lists.** A dozen arguments forwarded unchanged through
   layers. Pass one struct, or the request message itself.
8. **Background work in the wrong package.** Sweepers, dispatchers and loops live in their
   own package, not beside the RPC handlers; low-level packages never import command or
   handler packages.
9. **Comments that restate the code.** Delete them. A comment explains why: a constraint, a
   non-obvious decision, a bug it avoids.
10. **Exported but package-private, or dead.** Unexport what only its own package uses and
    delete what nothing calls.
11. **Tests that guard removed code.** A test asserting that a deleted call, field or log line
    stays absent verifies nothing once the code is gone. When a fix is a deletion, the diff is
    the evidence; test behaviour that still exists, never the absence of what was removed.

## Rules

- **Errors**: never discard or swallow one; handle it (log, count, return) exactly once.
  Return `res, nil` or `nil, err`, never `nil, nil` or a meaningful value alongside an error.
- **Concurrency**: synchronous first. `sync.Mutex` and `sync.WaitGroup` for shared state;
  channels for orchestration only. Every goroutine a function starts is waited for before
  the resources it uses close.
- **Panics**: validate input from outside the process (requests, stores, files, env) before
  dereferencing or indexing it. Skip nil checks on values the code itself controls. Prefer
  value fields over pointers.
- **Indentation**: handle errors and negative cases first and return early.
- **Layout**: declare identifiers in the file that uses them, as close to use as possible.
  No `util`, `common`, `helpers`, `misc`, `constants.go` or `types.go` grab-bags; name
  packages and files after their domain. A package's `errors.go` for shared sentinels is
  fine.
- **Order**: exported entry points at the top of a file, helpers below, ordered by
  importance, not dependency.
- **Names**: no type suffixes (`userMap`, `idStr`, `fooFn`); name length grows with scope.

## Reporting

For each problem give `file:line`, the item above it breaks, and the concrete fix. Skip
anything golangci-lint already reports.
