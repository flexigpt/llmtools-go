package pdfutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/flexigpt/llmtools-go/internal/logutil"
	"github.com/ledongthuc/pdf"
)

// ExtractPDFTextSafe extracts text from a local PDF with a byte limit and panic recovery.
func ExtractPDFTextSafe(ctx context.Context, path string, maxBytes int) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logutil.Warn("panic during PDF text extraction", "path", path, "panic", r)
			err = fmt.Errorf("panic during PDF text extraction: %v", r)
		}
	}()

	f, r, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	reader, err := r.GetPlainText()
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	limited := &io.LimitedReader{
		R: reader,
		N: int64(maxBytes),
	}
	if _, err := io.Copy(&buf, limited); err != nil {
		return "", err
	}
	text = strings.TrimSpace(buf.String())
	if text == "" {
		return "", errors.New("empty PDF text after extraction")
	}
	return text, nil
}
