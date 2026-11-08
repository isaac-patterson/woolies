# woolies

Go CLI for Woolies (search, cart, past orders). See `README.md` for commands, configuration and design.

- Use the Makefile (`make help` lists it; the README's Development section explains each target):
  - `make build` builds `bin/woolies`. `make install` also symlinks it onto the PATH.
  - `make test` runs the offline tests. Before finishing a change, run `make audit` (tests, tidy check, gofmt, vet, staticcheck, govulncheck); it must pass.
  - Coverage: run `go test -coverprofile=/tmp/woolies-coverage.out ./... && go tool cover -func=/tmp/woolies-coverage.out`. Don't run `make test/cover`, which opens a browser window.
  - `make tidy` rewrites code (`go fix`, `go fmt`), so review the diff afterwards. `make upgradeable` is read-only; don't act on its chromedp upgrade.
  - Don't run `make push` (it prompts and pushes) unless asked.
- Keep chromedp pinned at v0.14.2 with its matching `cdproto` and `go-json-experiment`. Newer chromedp has a different API.
- Settings (browser, profile, postcode, cupboard file, recipes dir) come from `~/.woolies/config.json` with env overrides (`cmd/woolies/config.go`). Never hard-code personal paths, addresses or postcodes in the code or docs; run `woolies config` to see the local values.
- Tests run offline: `cmd/woolies/fakesite_test.go` swaps `request` and `captureOrderBodies` for a fake Woolies API, so every command is tested without a browser. Add a test with the fake site for new commands. Only the code that drives the browser itself (launch, navigate, login, logout) is untested, so check it live. Only one process can use the profile at a time, so run live checks one at a time.
- Use read-only commands when testing. `add`, `add-many`, `set-qty`, `remove`, `clear` and `quick-add` change the signed-in account's real cart, so ask first.
- Never add checkout or payment, and never print cookies.
- `.claude/skills/` holds the `groceries`, `add-recipe` and `sous-chef` skills that drive this CLI (other projects use them through symlinks). If you change command names, flags or output fields, update those skills too. Keep them generic: no names, stores, postcodes or personal paths. Each user's details come from their grocery notes, the `CLAUDE.md` next to their `cupboard_file`.
- Personal, machine-specific notes go in `CLAUDE.local.md` (gitignored).
