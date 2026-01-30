package fileutil

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

type ReadEncoding string

const (
	ReadEncodingText   ReadEncoding = "text"
	ReadEncodingBinary ReadEncoding = "binary"
)

// ReadFile reads a file and returns its contents.
// If maxBytes > 0, it enforces a hard cap during reading.
func ReadFile(path string, encoding ReadEncoding, maxBytes int64) (string, error) {
	if path == "" {
		return "", ErrInvalidPath
	}

	if encoding != ReadEncodingText && encoding != ReadEncodingBinary {
		return "", errors.New(`encoding must be "text" or "binary"`)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	r := io.Reader(f)
	if maxBytes > 0 {
		r = io.LimitReader(f, maxBytes+1)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}

	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return "", fmt.Errorf("file %q exceeds maximum allowed size (%d bytes)", path, maxBytes)
	}

	if encoding == ReadEncodingText {
		return string(data), nil
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
