//go:build race

package download

// raceEnabled skips the 4 GiB zip64 stream under -race (CRC32 over 4 GiB
// takes > 1 minute with the race detector; 1.6 s without it).
const raceEnabled = true
