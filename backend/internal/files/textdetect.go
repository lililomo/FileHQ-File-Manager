package files

// Binary detection for the editor (SPEC §8.13 "Membuka file" step 2),
// following git's heuristic: a NUL byte in the first 8000 bytes means
// binary; otherwise a sample whose "non-text" bytes (control characters
// other than TAB/LF/CR/FF/BS/ESC, and DEL) exceed 30 % is binary too.
// UTF-16 content is decided by its BOM before this runs (it is full of NUL).

// BinarySample is the number of leading bytes IsBinary inspects.
const BinarySample = 8000

// IsBinary reports whether the leading bytes look like binary content.
func IsBinary(data []byte) bool {
	if len(data) > BinarySample {
		data = data[:BinarySample]
	}
	if len(data) == 0 {
		return false
	}
	nonText := 0
	for _, b := range data {
		switch {
		case b == 0:
			return true
		case b < 0x20:
			switch b {
			case '\t', '\n', '\r', '\f', '\b', 0x1b:
			default:
				nonText++
			}
		case b == 0x7f:
			nonText++
		}
	}
	return nonText*100 > len(data)*30
}
