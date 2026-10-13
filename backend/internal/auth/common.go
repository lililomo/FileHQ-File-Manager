package auth

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"slices"
	"strings"
	"sync"
)

// common_passwords.txt.gz: SecLists "Pwdb_top-100000.txt" (MIT, see NOTICE),
// lower-cased, de-duplicated and sorted so lookups are a binary search.
//
//go:embed common_passwords.txt.gz
var commonPasswordsGz []byte

var (
	commonOnce sync.Once
	commonList []string
)

func loadCommon() {
	zr, err := gzip.NewReader(bytes.NewReader(commonPasswordsGz))
	if err != nil {
		return
	}
	defer func() { _ = zr.Close() }()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	list := make([]string, 0, 100000)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			list = append(list, line)
		}
	}
	if !slices.IsSorted(list) {
		slices.Sort(list)
	}
	commonList = list
}

// IsCommonPassword reports whether password (case-insensitively) is in the
// embedded list of common passwords.
func IsCommonPassword(password string) bool {
	commonOnce.Do(loadCommon)
	_, found := slices.BinarySearch(commonList, strings.ToLower(strings.TrimSpace(password)))
	return found
}

// CommonPasswordCount returns the number of entries in the embedded list.
func CommonPasswordCount() int {
	commonOnce.Do(loadCommon)
	return len(commonList)
}
