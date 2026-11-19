## What this changes

<!-- One or two sentences. If it fixes an issue: "Fixes #123". -->

## Why

<!-- Skip if it is obvious from the above. -->

## AI tools

<!-- Required. "None", or which tool and what it did (drafted the change, wrote the tests, explained the code, wrote this description). See CONTRIBUTING.md. -->

## How I tested it

- [ ] `go test ./...`
- [ ] Ran it between Pat and a real modem (if the proxy changed)

## Checklist

- [ ] `gofmt -l .` prints nothing and `go vet ./...` is clean
- [ ] The proxy still forwards every byte unchanged and logs no message contents
- [ ] `CHANGELOG.md` entry under `[Unreleased]`, if this is user-visible
- [ ] The "AI tools" section above is filled in, and I have read and understood everything I am submitting
- [ ] I accept the [Contributor License Agreement](../CLA.md) (the CLA check will ask on a first pull request)
