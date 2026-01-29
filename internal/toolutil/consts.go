package toolutil

const GOOSWindows = "windows"

const maxToolBytes = 16 * 1024 * 1024 // 16MB

// MaxTextProcessingBytes caps loading/editing text files for line-based tools.
const MaxTextProcessingBytes = maxToolBytes

// MaxFileReadBytes caps raw bytes read from disk by “read file” style tools.
const MaxFileReadBytes = maxToolBytes

// MaxFileWriteBytes caps raw bytes written to disk by “write file” style tools.
const MaxFileWriteBytes = maxToolBytes
