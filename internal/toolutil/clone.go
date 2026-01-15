package toolutil

import (
	"bytes"

	"github.com/flexigpt/llmtools-go/spec"
)

func CloneTool(t spec.Tool) spec.Tool {
	// ArgSchema is json.RawMessage ([]byte) => must deep copy.
	if len(t.ArgSchema) > 0 {
		t.ArgSchema = bytes.Clone(t.ArgSchema)
	}
	// Tags is a slice => must deep copy.
	if len(t.Tags) > 0 {
		t.Tags = append([]string(nil), t.Tags...)
	}
	return t
}
