# Contributing

Thank you for contributing.

## Before opening a pull request

- Keep external actions dry-run by default and call out any safety implications.
- Add or update tests for behavior changes.
- Run `gofmt -w` on changed Go files, then `go mod tidy -diff`, `go vet ./...`, and `go test -race ./...`.
- Run `actionlint` after changing GitHub Actions workflows.
- Check that no configuration, database, binary, credential, token, or personal media path is staged.
- Update documentation and release notes when users will notice a change.

Use conventional, human-authored commit messages. Do not add agent or LLM co-author trailers/attribution.

## Security

Never commit credentials, access tokens, personal media paths, production configuration, or local databases. Follow [SECURITY.md](SECURITY.md) instead of filing a public vulnerability issue.
