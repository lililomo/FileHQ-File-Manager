//go:build race

package archive

// raceEnabled relaxes the wall-clock gates of the 50k-entry index test under
// -race (everything runs several times slower there, as in internal/listing).
func init() { raceEnabled = true }
