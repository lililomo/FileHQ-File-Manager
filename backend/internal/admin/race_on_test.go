//go:build race

package admin

// raceEnabled shrinks the 100k-row export test under -race (modernc SQLite
// is ~30x slower when instrumented); `go test ./internal/admin -run Export100k`
// without -race runs the full size.
const raceEnabled = true
