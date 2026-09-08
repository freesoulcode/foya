# Contributing

Foya is maintained in limited spare time. Please keep changes focused and
provide enough information for them to be reviewed and verified directly.

## Before You Start

- Search the existing [issues](https://github.com/freesoulcode/foya/issues).
- Small fixes may be submitted directly as a pull request.
- For larger features or architectural changes, open an issue describing the
  use case first.
- Report vulnerabilities privately according to the [security policy](./SECURITY.md).

## Local Development

```bash
make fe-install
make desktop-dev
```

Common checks:

```bash
make fmt
make vet
make test
make fe-build
make site-check
make site-build
```

Run the checks relevant to your changes. If you cannot run one, explain why in
the pull request.

## Submission Guidelines

- Keep each pull request focused on one problem.
- Add tests and documentation for behavior changes when needed.
- Include screenshots or recordings for UI changes.
- Do not commit secrets, tokens, local data, caches, or build artifacts.

Contributions are licensed under the [Apache License 2.0](./LICENSE).
