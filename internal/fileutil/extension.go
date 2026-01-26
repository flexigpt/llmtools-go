package fileutil

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidPath      = errors.New("invalid path")
	ErrUnknownExtension = errors.New("unknown extension")
)

type ExtensionMode string

const (
	ExtensionModeText     ExtensionMode = "text"
	ExtensionModeImage    ExtensionMode = "image"
	ExtensionModeDocument ExtensionMode = "document"
	ExtensionModeDefault  ExtensionMode = "default"
)

var AllExtensionModes = []ExtensionMode{
	ExtensionModeText,
	ExtensionModeImage,
	ExtensionModeDocument,
	ExtensionModeDefault,
}

type FileExt string

const (
	ExtTxt      FileExt = ".txt"
	ExtMd       FileExt = ".md"
	ExtMarkdown FileExt = ".markdown"
	ExtLog      FileExt = ".log"
	ExtJSON     FileExt = ".json"
	ExtYAML     FileExt = ".yaml"
	ExtYML      FileExt = ".yml"
	ExtTOML     FileExt = ".toml"
	ExtJS       FileExt = ".js"
	ExtTS       FileExt = ".ts"
	ExtTSX      FileExt = ".tsx"
	ExtJSX      FileExt = ".jsx"
	ExtPY       FileExt = ".py"
	ExtGO       FileExt = ".go"
	ExtRS       FileExt = ".rs"
	ExtJAVA     FileExt = ".java"
	ExtC        FileExt = ".c"
	ExtCPP      FileExt = ".cpp"
	ExtH        FileExt = ".h"
	ExtHPP      FileExt = ".hpp"
	ExtCS       FileExt = ".cs"
	ExtRB       FileExt = ".rb"
	ExtPHP      FileExt = ".php"
	ExtHTML     FileExt = ".html"
	ExtHTM      FileExt = ".htm"
	ExtCSS      FileExt = ".css"
	ExtSCSS     FileExt = ".scss"
	ExtLESS     FileExt = ".less"
	ExtSQL      FileExt = ".sql"
	ExtMod      FileExt = ".mod"
	ExtSum      FileExt = ".sum"
	ExtJSONL    FileExt = ".jsonl"
	ExtShell    FileExt = ".sh"
	ExtSWIFT    FileExt = ".swift"
	ExtM        FileExt = ".m"
	ExtKT       FileExt = ".kt"
	ExtPL       FileExt = ".pl"
	ExtSCALA    FileExt = ".scala"
	ExtHS       FileExt = ".hs"
	ExtLUA      FileExt = ".lua"
	ExtDART     FileExt = ".dart"
	ExtCmake    FileExt = ".cmake"
	ExtBazel    FileExt = ".bazel"
	ExtXML      FileExt = ".xml"

	ExtJPG  FileExt = ".jpg"
	ExtJPEG FileExt = ".jpeg"
	ExtPNG  FileExt = ".png"
	ExtGIF  FileExt = ".gif"
	ExtWEBP FileExt = ".webp"
	ExtBMP  FileExt = ".bmp"
	ExtSVG  FileExt = ".svg"

	ExtPDF  FileExt = ".pdf"
	ExtDOC  FileExt = ".doc"
	ExtDOCX FileExt = ".docx"
	ExtPPT  FileExt = ".ppt"
	ExtPPTX FileExt = ".pptx"
	ExtXLS  FileExt = ".xls"
	ExtXLSX FileExt = ".xlsx"
	ExtODT  FileExt = ".odt"
	ExtODS  FileExt = ".ods"
)

type MIMEType string

const (
	MIMEEmpty                  MIMEType = ""
	MIMEApplicationOctetStream MIMEType = "application/octet-stream"

	MIMETextPlain    MIMEType = "text/plain; charset=utf-8"
	MIMETextMarkdown MIMEType = "text/markdown; charset=utf-8"
	MIMETextHTML     MIMEType = "text/html; charset=utf-8"
	MIMETextCSS      MIMEType = "text/css; charset=utf-8"

	MIMEApplicationJSON MIMEType = "application/json"
	MIMEApplicationXML  MIMEType = "application/xml"
	MIMEApplicationYAML MIMEType = "application/x-yaml"
	MIMEApplicationTOML MIMEType = "application/toml"
	MIMEApplicationSQL  MIMEType = "application/sql"
	MIMEApplicationJS   MIMEType = "application/javascript"

	MIMEImageJPEG MIMEType = "image/jpeg"
	MIMEImagePNG  MIMEType = "image/png"
	MIMEImageGIF  MIMEType = "image/gif"
	MIMEImageWEBP MIMEType = "image/webp"
	MIMEImageBMP  MIMEType = "image/bmp"
	MIMEImageSVG  MIMEType = "image/svg+xml"

	MIMEApplicationPDF        MIMEType = "application/pdf"
	MIMEApplicationMSWord     MIMEType = "application/msword"
	MIMEApplicationMSPowerPt  MIMEType = "application/vnd.ms-powerpoint"
	MIMEApplicationMSExcel    MIMEType = "application/vnd.ms-excel"
	MIMEApplicationOpenXMLDoc MIMEType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	MIMEApplicationOpenXMLPPT MIMEType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	MIMEApplicationOpenXMLXLS MIMEType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	MIMEApplicationODT        MIMEType = "application/vnd.oasis.opendocument.text"
	MIMEApplicationODS        MIMEType = "application/vnd.oasis.opendocument.spreadsheet"
)

// ExtensionToMIMEType is an internal registry of common/explicitly-supported extensions.
// This is used before falling back to mime.TypeByExtension.
var ExtensionToMIMEType = map[FileExt]MIMEType{
	ExtTxt:      MIMETextPlain,
	ExtMd:       MIMETextMarkdown,
	ExtMarkdown: MIMETextMarkdown,
	ExtLog:      MIMETextPlain,
	ExtJSON:     MIMEApplicationJSON,
	ExtYAML:     MIMEApplicationYAML,
	ExtYML:      MIMEApplicationYAML,
	ExtTOML:     MIMEApplicationTOML,
	ExtJS:       MIMEApplicationJS,
	ExtTS:       MIMETextPlain,
	ExtTSX:      MIMETextPlain,
	ExtJSX:      MIMETextPlain,
	ExtPY:       MIMETextPlain,
	ExtGO:       MIMETextPlain,
	ExtRS:       MIMETextPlain,
	ExtJAVA:     MIMETextPlain,
	ExtC:        MIMETextPlain,
	ExtCPP:      MIMETextPlain,
	ExtH:        MIMETextPlain,
	ExtHPP:      MIMETextPlain,
	ExtCS:       MIMETextPlain,
	ExtRB:       MIMETextPlain,
	ExtPHP:      MIMETextPlain,
	ExtHTML:     MIMETextHTML,
	ExtHTM:      MIMETextHTML,
	ExtCSS:      MIMETextCSS,
	ExtSCSS:     MIMETextPlain,
	ExtLESS:     MIMETextPlain,
	ExtSQL:      MIMEApplicationSQL,
	ExtMod:      MIMETextPlain,
	ExtSum:      MIMETextPlain,
	ExtJSONL:    MIMETextPlain,
	ExtShell:    MIMETextPlain,
	ExtSWIFT:    MIMETextPlain,
	ExtM:        MIMETextPlain,
	ExtKT:       MIMETextPlain,
	ExtPL:       MIMETextPlain,
	ExtSCALA:    MIMETextPlain,
	ExtHS:       MIMETextPlain,
	ExtLUA:      MIMETextPlain,
	ExtDART:     MIMETextPlain,
	ExtCmake:    MIMETextPlain,
	ExtBazel:    MIMETextPlain,
	ExtXML:      MIMEApplicationXML,

	ExtJPG:  MIMEImageJPEG,
	ExtJPEG: MIMEImageJPEG,
	ExtPNG:  MIMEImagePNG,
	ExtGIF:  MIMEImageGIF,
	ExtWEBP: MIMEImageWEBP,
	ExtBMP:  MIMEImageBMP,
	ExtSVG:  MIMEImageSVG,

	ExtPDF:  MIMEApplicationPDF,
	ExtDOC:  MIMEApplicationMSWord,
	ExtDOCX: MIMEApplicationOpenXMLDoc,
	ExtPPT:  MIMEApplicationMSPowerPt,
	ExtPPTX: MIMEApplicationOpenXMLPPT,
	ExtXLS:  MIMEApplicationMSExcel,
	ExtXLSX: MIMEApplicationOpenXMLXLS,
	ExtODT:  MIMEApplicationODT,
	ExtODS:  MIMEApplicationODS,
}

// BaseMIMEToMode maps *base mime types* (no parameters) to a coarse mode.
var BaseMIMEToMode = map[string]ExtensionMode{
	"":                         ExtensionModeDefault,
	"application/octet-stream": ExtensionModeDefault,

	// Text.
	"text/plain":             ExtensionModeText,
	"text/markdown":          ExtensionModeText,
	"text/html":              ExtensionModeText,
	"text/css":               ExtensionModeText,
	"application/json":       ExtensionModeText,
	"application/xml":        ExtensionModeText,
	"application/x-yaml":     ExtensionModeText,
	"application/yaml":       ExtensionModeText,
	"application/toml":       ExtensionModeText,
	"application/sql":        ExtensionModeText,
	"application/javascript": ExtensionModeText,

	// Images.
	"image/jpeg":    ExtensionModeImage,
	"image/png":     ExtensionModeImage,
	"image/gif":     ExtensionModeImage,
	"image/webp":    ExtensionModeImage,
	"image/bmp":     ExtensionModeImage,
	"image/svg+xml": ExtensionModeImage,

	// Documents.
	"application/pdf":               ExtensionModeDocument,
	"application/msword":            ExtensionModeDocument,
	"application/vnd.ms-powerpoint": ExtensionModeDocument,
	"application/vnd.ms-excel":      ExtensionModeDocument,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ExtensionModeDocument,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ExtensionModeDocument,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ExtensionModeDocument,
	"application/vnd.oasis.opendocument.text":                                   ExtensionModeDocument,
	"application/vnd.oasis.opendocument.spreadsheet":                            ExtensionModeDocument,
}

// ModeToExtensions is a convenience reverse index built from ExtensionToMIMEType + BaseMIMEToMode.
var ModeToExtensions = func() map[ExtensionMode][]FileExt {
	m := make(map[ExtensionMode][]FileExt, len(AllExtensionModes))
	for ext, mt := range ExtensionToMIMEType {
		mode := modeForMIME(mt)
		m[mode] = append(m[mode], ext)
	}
	return m
}()

// MIMEForLocalFile returns a best-effort MIME type and a coarse "mode" (text/image/document/default).
//
// Behavior:
//   - First try extension-based detection (internal registry + stdlib).
//   - If extension detection is unknown or generic, sniff the file bytes.
//   - Sniffing uses DetectContentType + a small "isProbablyTextSample" heuristic.
func MIMEForLocalFile(path string) (mimeType MIMEType, mode ExtensionMode, err error) {
	if strings.TrimSpace(path) == "" {
		return MIMEEmpty, ExtensionModeDefault, ErrInvalidPath
	}

	ext := filepath.Ext(path)
	if ext != "" {
		mt, e := MIMEFromExtensionString(ext)
		if e == nil && mt != MIMEEmpty && baseMIME(mt) != string(MIMEApplicationOctetStream) {
			return mt, modeForMIME(mt), nil
		}
		// Unknown or generic => sniff.
	}

	return SniffFileMIME(path)
}

// MIMEFromExtensionString returns a best-known MIME for the given extension string.
// Accepts "png" as well as ".png" (useful because image.DecodeConfig returns "png").
//
// Lookup order: internal registry -> stdlib mime.TypeByExtension.
// If the extension cannot be resolved, returns application/octet-stream and ErrUnknownExtension.
func MIMEFromExtensionString(ext string) (MIMEType, error) {
	if strings.TrimSpace(ext) == "" {
		return MIMEEmpty, ErrInvalidPath
	}

	e := normalizeExt(ext)
	if e == "" {
		return MIMEEmpty, ErrInvalidPath
	}

	if mt, ok := ExtensionToMIMEType[e]; ok {
		return mt, nil
	}

	// Fall back to stdlib mapping.
	if t := mime.TypeByExtension(string(e)); t != "" {
		return MIMEType(t), nil
	}

	return MIMEApplicationOctetStream, ErrUnknownExtension
}

// SniffFileMIME inspects initial bytes of a file and returns a best-effort
// MIME type and mode. It will return an error if the file can't be opened/read.
func SniffFileMIME(path string) (mimeType MIMEType, mode ExtensionMode, err error) {
	if strings.TrimSpace(path) == "" {
		return MIMEEmpty, ExtensionModeDefault, ErrInvalidPath
	}

	f, err := os.Open(path)
	if err != nil {
		return MIMEEmpty, ExtensionModeDefault, err
	}
	defer f.Close()

	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return MIMEEmpty, ExtensionModeDefault, err
	}

	sample := buf[:n]
	if len(sample) == 0 {
		// Empty file: treat as text.
		return MIMETextPlain, ExtensionModeText, nil
	}

	mt := MIMEType(http.DetectContentType(sample))
	m := modeForMIME(mt)

	// If DetectContentType is generic, try to classify text via heuristic.
	if baseMIME(mt) == string(MIMEApplicationOctetStream) || mt == MIMEEmpty {
		if isProbablyTextSample(sample) {
			return MIMETextPlain, ExtensionModeText, nil
		}
		return MIMEApplicationOctetStream, ExtensionModeDefault, nil
	}

	// If DetectContentType says "text/plain" but the sample is clearly binary,
	// downgrade to default/octet-stream.
	if m == ExtensionModeText && !isProbablyTextSample(sample) {
		return MIMEApplicationOctetStream, ExtensionModeDefault, nil
	}

	return mt, m, nil
}

func modeForMIME(mt MIMEType) ExtensionMode {
	base := baseMIME(mt)
	if m, ok := BaseMIMEToMode[base]; ok {
		return m
	}

	// Heuristics for unknown but structured text-like types.
	switch {
	case strings.HasPrefix(base, "text/"):
		return ExtensionModeText
	case strings.HasPrefix(base, "image/"):
		return ExtensionModeImage
	}

	// Structured suffixes like application/vnd.foo+json.
	if strings.HasSuffix(base, "+json") || strings.HasSuffix(base, "+xml") {
		return ExtensionModeText
	}

	return ExtensionModeDefault
}

func baseMIME(mt MIMEType) string {
	s := strings.TrimSpace(strings.ToLower(string(mt)))
	if s == "" {
		return ""
	}
	// Drop parameters like "; charset=utf-8".
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// isProbablyTextSample returns true if the byte sample looks like text.
// Heuristic: disallow NULs and too many control bytes (except \t, \n, \r).
func isProbablyTextSample(p []byte) bool {
	if len(p) == 0 {
		return true
	}
	nulCount := 0
	controlCount := 0
	for _, b := range p {
		if b == 0 {
			nulCount++
			continue
		}
		if b < 32 && b != 9 && b != 10 && b != 13 {
			controlCount++
		}
	}
	if nulCount > 0 {
		return false
	}
	// If >10% bytes are control chars, assume binary.
	return controlCount*10 <= len(p)
}

// normalizeExt lowercases and ensures a leading '.' for an extension.
func normalizeExt(ext string) FileExt {
	e := strings.TrimSpace(ext)
	if e == "" {
		return FileExt("")
	}
	if !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	return FileExt(strings.ToLower(e))
}
