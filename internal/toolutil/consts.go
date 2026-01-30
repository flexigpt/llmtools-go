package toolutil

const (
	GOOSLinux   = "linux"
	GOOSWindows = "windows"
	GOOSDarwin  = "darwin"

	GOOSFreebsd   = "freebsd"
	GOOSOpenbsd   = "openbsd"
	GOOSNetbsd    = "netbsd"
	GOOSDragonfly = "dragonfly"
)

const maxToolBytes = 16 * 1024 * 1024 // 16MB

// MaxTextProcessingBytes caps loading/editing text files for line-based tools.
const MaxTextProcessingBytes = maxToolBytes

// MaxFileReadBytes caps raw bytes read from disk by “read file” style tools.
const MaxFileReadBytes = maxToolBytes

// MaxFileWriteBytes caps raw bytes written to disk by “write file” style tools.
const MaxFileWriteBytes = maxToolBytes
