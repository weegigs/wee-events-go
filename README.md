# wee-events

// todo: write intro

wee-events, **`we`**

## Tools

This project uses [mise](https://mise.jdx.dev) to manage its toolchain
(Go 1.26, golangci-lint, gopls, natscli, just). Install it, then provision:

```sh
mise install
```

## Getting started

Run the unit tests to ensure everything is 🕺

```sh
just test
```

Tasks live in the `justfile`; run `just --list` for the full set (`build`,
`wire`, `lint`, `fix`, `update-deps`, `test-integration`).

## Documentation

In the `documents` directory you'll find notes on

[golang]: https://go.dev/doc/go1.26
