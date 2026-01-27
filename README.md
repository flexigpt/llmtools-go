# LLM Tools for Go

[![License: MIT](https://img.shields.io/badge/License-MIT-brightgreen.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/flexigpt/llmtools-go)](https://goreportcard.com/report/github.com/flexigpt/llmtools-go)
[![lint](https://github.com/flexigpt/llmtools-go/actions/workflows/lint.yml/badge.svg?branch=main)](https://github.com/flexigpt/llmtools-go/actions/workflows/lint.yml)
[![test](https://github.com/ppipada/llmtools-go/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/ppipada/llmtools-go/actions/workflows/test.yml)

LLM Tool implementations for Golang

- [Features at a glance](#features-at-a-glance)
- [Package overview](#package-overview)
- [Installation](#installation)
- [Quickstart](#quickstart)
  - [Registry with Built-ins](#registry-with-built-ins)
  - [Direct Tool Usage](#direct-tool-usage)
- [Shell Tool Notes](#shell-tool-notes)
- [Development](#development)
- [License](#license)

## Features at a glance

- Go-native tool implementations for common local tasks. Current tools:
  - File system (`fstool`):
    - List directory (`listdir`): Lists entries under a directory, optionally filtered via glob.
    - Read file (`readfile`): Reads local files as UTF-8 text (rejects non-text content) or base64 binary (with image/file output kinds). Includes a size cap for safety.
    - Search files (`searchfiles`): Recursively searches path and (text) content using RE2 regex.
    - Inspect path (`statpath`): Returns existence, size, timestamps, and directory flag.

  - Images (`imagetool`):
    - Read image (`readimage`): Read intrinsic metadata for a local image file, optionally including base64-encoded contents.

  - Commands (`shelltool`):
    - Execute Shell commands (`shell`): Execute local shell commands (cross-platform) with timeouts, output caps, and session-like persistence for workdir/env. (Check notes below too).

- Tool registry for:
  - collecting and listing tool manifests (stable ordering)
  - invoking tools via JSON input/output with strict JSON input decoding
  - tool call timeout handling

## Package overview

- `llmtools`: Registry and registration helpers
- `spec`: Tool manifests + IO/output schema
- `fstool`: Filesystem tools.
- `imagetool`: Image tools.
- `shelltool`: Shell tools.

## Installation

```bash
# Go 1.25+
go get github.com/flexigpt/llmtools-go
```

## Quickstart

### Registry with Built-ins

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/flexigpt/llmtools-go"
    "github.com/flexigpt/llmtools-go/spec"
)

func main() {
    r, err := llmtools.NewBuiltinRegistry(
        llmtools.WithCallTimeoutForAll(10*time.Minute),
    )
    if err != nil {
        panic(err)
    }

    // List tool manifests (for prompt/tool definition)
    for _, t := range r.Tools() {
        fmt.Printf("%s (%s): %s\n", t.Slug, t.GoImpl.FuncID, t.Description)
    }

    // Call a tool by FuncID using JSON input
    in := json.RawMessage(`{"path": ".", "pattern": "*.go"}`)
    out, err := r.Call(context.Background(), spec.FuncID("..."), in)
    if err != nil {
        panic(err)
    }

    fmt.Printf("tool outputs: %+v\n", out)
}
```

### Direct Tool Usage

```go
package main

import (
    "context"
    "fmt"

    "github.com/flexigpt/llmtools-go/fstool"
)

func main() {
    out, err := fstool.ListDirectory(context.Background(), fstool.ListDirectoryArgs{
        Path:    ".",
        Pattern: "*.md",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(out.Entries)
}
```

## Shell Tool Notes

- OS support:
  - Uses Go build constraints (`windows` / `!windows`) to select process-group handling.
  - No consumer build tags are required.

- Timeouts:
  - The tool enforces its own per-command timeout via `timeoutMS`.
  - If you also set a registry-level timeout (`WithDefaultCallTimeout` or `WithCallTimeout`),
    ensure it is >= the tool timeout or set it to 0 to avoid early cancellation.

- Policy knobs:
  - Hosts can pass a policy into tool instantiation. The default policy is at: `shelltool.DefaultShellCommandPolicy`.

## Development

- Formatting follows `gofumpt` and `golines` via `golangci-lint`, which is also used for linting. All rules are in [.golangci.yml](.golangci.yml).
- Useful scripts are defined in `taskfile.yml`; requires [Task](https://taskfile.dev/).
- Bug reports and PRs are welcome:
  - Keep the public API (`package llmtools` and `spec`) small and intentional.
  - Avoid leaking provider‑specific types through the public surface; put them under `internal/`.
  - Please run tests and linters before sending a PR.

## License

Copyright (c) 2026 - Present - Pankaj Pipada

All source code in this repository, unless otherwise noted, is licensed under the MIT License.
See [LICENSE](./LICENSE) for details.
