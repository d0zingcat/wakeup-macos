# Project Rules

## Language

- All commit messages must be in English
- All PR titles and descriptions must be in English
- All release notes must be in English
- All code comments must be in English
- All documentation (README, CHANGELOG, etc.) must be in English

## Commit Convention

- Use conventional commits: `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`
- Keep the subject line concise (≤72 chars)
- Use imperative mood ("add feature" not "added feature")

## Workflow

- Do not commit directly on main without explicit permission
- Run `go build ./...` and `go test ./...` before committing
- Run `go vet ./...` before pushing
