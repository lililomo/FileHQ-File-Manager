package files

import (
	"io/fs"
	"syscall"
)

func devOf(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Dev
	}
	return 0
}
