# Contributing

Thank you for contributing. By participating, you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Proposing a change

- Search existing issues before opening a new one.
- Use the bug or feature template and remove credentials, tokens, private media
  paths, and other personal data.
- Open an issue before investing in a large feature or a change that adds a new
  external write action.

## Development

1. Fork the repository and create a focused branch from `main`.
2. Install the Go toolchain declared in `go.mod`.
3. Make one focused change with tests and relevant documentation.
4. Sign the commit and open a pull request against `main`.

## Before opening a pull request

- Keep external actions dry-run by default and call out any safety implications.
- Add or update tests for behavior changes.
- Run `gofmt -w` on changed Go files, then `go mod tidy -diff`, `go vet ./...`, and `go test -race ./...`.
- Run `actionlint` after changing GitHub Actions workflows.
- Check that no configuration, database, binary, credential, token, or personal media path is staged.
- Update documentation and release notes when users will notice a change.
- Keep the pull request focused and resolve all review conversations.

Use conventional, human-authored commit messages. Do not add agent or LLM co-author trailers/attribution.

## Security

Never commit credentials, access tokens, personal media paths, production configuration, or local databases. Follow [SECURITY.md](SECURITY.md) instead of filing a public vulnerability issue.
