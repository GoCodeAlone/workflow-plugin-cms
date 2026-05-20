# Contributing to workflow-plugin-cms

This plugin is part of the [GoCodeAlone/workflow](https://github.com/GoCodeAlone/workflow) ecosystem.

## Before contributing

Read the [upstream CONTRIBUTING.md](https://github.com/GoCodeAlone/workflow/blob/main/CONTRIBUTING.md) for general conventions, signing, and review expectations.

## Local development

If you have a `go.work` file in a parent directory (multi-repo workspace), use `GOWORK=off`:

```sh
git clone https://github.com/GoCodeAlone/workflow-plugin-cms.git
cd workflow-plugin-cms
GOWORK=off go build ./...
GOWORK=off go test ./...
```

## Pull requests

- One feature or bugfix per PR.
- Update CHANGELOG.md with a Keep-a-Changelog entry.
- Add tests covering new behaviour.
- Run `GOWORK=off go vet ./...` before pushing.

## Reporting issues

See the issue templates under `.github/ISSUE_TEMPLATE/`.
