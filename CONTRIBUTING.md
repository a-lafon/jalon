# Contributing

Pull requests are written in English, title included; the [template](.github/pull_request_template.md) asks for why, what changes, the checks run and the known limits.

## Checks

```sh
gofmt -l .              # lists unformatted files; gofmt -w . fixes them
go vet ./...
go test -race ./...
golangci-lint run       # config in .golangci.yml
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

CI runs the same checks on every push to `main` and on every pull request.

`go test -tags e2e ./cmd/jalon` runs the integration tests: the real Claude Code, in `claude -p`, runs Jalon's hooks against a fake model API the test serves, so they need `claude` on the `PATH` but no key, no model and no network beyond localhost. They check what Jalon sends the model and the transcript fields it reads, not what a model does with the advice. CI runs them with a pinned Claude Code on every pull request, and with the latest one every night.

`go test ./internal/handoff -run=XXX -fuzz=FuzzContainment -fuzztime=60s` fuzzes the scanner that reads handoff paths out of a model's text, and the same command on `./internal/client/claudecode` with `-fuzz=FuzzScanTail` fuzzes the read of a transcript's end. Both parse input Jalon does not choose the shape of, so run them after changing one. Their seeds run as ordinary tests, so CI covers those on every push; the fuzzer itself is manual. An input that fails is written to `testdata/fuzz/` and fails every later `go test`: commit it with the fix.

## Demo GIF

The GIF at the top of the README is a real Claude Code session. [docs/demo](docs/demo/README.md) has what it takes to record it again when the advice changes.
