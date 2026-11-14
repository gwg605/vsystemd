# vsystemd Development Process

---

## Branching

- `master` — stable, reviewed code only
- Every task gets its own branch: `feature/phase-X.Y-description`, `fix/description`
- Each branch requires user review before merge
- Merge to master with `--no-ff` only after explicit user approval
- Feature branches are not deleted after merge — keep them for history and reference

---

## Code Style

- Minimal comments in code and tests — code should be self-explanatory
- In interfaces: prefer custom types over primitives
- In implementations: use types that naturally fit the logic of the package; avoid unnecessary casts
- No dead code, no commented-out code

---

## Tests

- Unit tests cover happy path and minimal error logic
- No boundary/edge case tests — code may change significantly and we don't want tests tightly coupled to current logic
- Tests are written alongside the code, not after
- Windows-specific tests (SCM, named pipes) use `//go:build windows` build tag
- Fake implementations live in `fake/` subdirectory of their respective package

---

## CI

- GitHub Actions with `windows-latest` runner
- CI runs `go test ./...`; current integration tests use fake SCM/storage and the `//go:build windows` tag
- Tests that require a real Windows SCM must use the additional `//go:build integration` tag and are excluded from CI
- Real SCM integration tests are run manually on a local Windows machine
- CI must pass before merge

---

## Conflict Resolution

- If a logical conflict or ambiguity is discovered during implementation that was not visible at planning stage — stop, report to user, wait for decision
- Do not resolve design conflicts unilaterally
- Do not add complexity without user confirmation

---

## Dependencies

- Prefer minimal, well-maintained dependencies
- CLI: `github.com/akamensky/argparse`
- Calendar parsing: `gitlab.com/allddd/go-systemd-time`
- Windows APIs: `golang.org/x/sys`
- Named pipes: `github.com/Microsoft/go-winio`
- No CGO

---

## Workflow per Task

1. Create branch `feature/phase-X.Y-description`
2. Implement + tests
3. Verify build and tests pass
4. Present to user for review
5. After approval: merge to master with `--no-ff`
6. Keep the feature branch after merge — do not delete it
