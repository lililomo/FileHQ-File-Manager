package listing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"golang.org/x/text/collate"

	"kppbwi.app/filemanager/internal/fsjail"
)

// ErrBadCursor is returned for cursors that are malformed, forged or made
// for a different sort specification (API: 400 VALIDATION_FAILED).
var ErrBadCursor = errors.New("listing: invalid cursor")

const cursorMACLen = 8

// Signer produces opaque cursors (SPEC §6.1): base64url(JSON || HMAC-8).
// The MAC prevents tampering; the payload is not secret.
type Signer struct{ key []byte }

// NewSigner derives the cursor key from the application secret.
func NewSigner(secret []byte) *Signer {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("listing-cursor-v1"))
	return &Signer{key: mac.Sum(nil)}
}

type cursorPayload struct {
	V  int             `json:"v"`
	S  string          `json:"s"` // SortSpec.String()
	K  json.RawMessage `json:"k,omitempty"`
	K2 json.RawMessage `json:"k2,omitempty"`
	N  string          `json:"n"`
	P  bool            `json:"p"`
	I  bool            `json:"i"`
}

func encodeKey(f Field, it *Item) json.RawMessage {
	if f == "" {
		return nil
	}
	v, ok := value(f, it)
	if !ok {
		return json.RawMessage("null")
	}
	switch f {
	case FieldName, FieldType, FieldExt, FieldNote:
		b, _ := json.Marshal(v.s)
		return b
	}
	return json.RawMessage(strconv.FormatInt(v.i, 10))
}

func applyKey(f Field, raw json.RawMessage, it *Item) error {
	if f == "" || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	switch f {
	case FieldName:
		return nil // name travels in N
	case FieldType:
		return json.Unmarshal(raw, &it.TypeLabel)
	case FieldExt:
		return json.Unmarshal(raw, &it.Ext)
	case FieldNote:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		it.Note = &s
		return nil
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return err
	}
	switch f {
	case FieldSize:
		if it.IsDir {
			it.ItemCount = &n
		} else {
			it.Size = n
		}
	case FieldMtime:
		it.mtime = unixNano(n)
	case FieldAtime:
		it.atime = unixNano(n)
	case FieldBtime:
		t := unixNano(n)
		it.btime = &t
	case FieldMode:
		if n < 0 || n > 0o7777 {
			return ErrBadCursor
		}
		it.modeNum = uint32(n) //nolint:gosec // bounded to 12 permission bits above
	case FieldDimensions:
		it.Media = &Media{Width: 1, Height: int(n)}
	case FieldDuration:
		it.Media = &Media{DurationMs: n}
	}
	return nil
}

func unixNano(n int64) time.Time { return time.Unix(0, n) }

// Encode builds the cursor pointing just after last.
func (s *Signer) Encode(spec SortSpec, last *Item) string {
	p := cursorPayload{V: 1, S: spec.String(), N: last.Name, P: last.Pinned, I: last.IsDir,
		K: encodeKey(spec.Field, last), K2: encodeKey(spec.Field2, last)}
	body, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, s.key)
	mac.Write(body)
	out := append(body, mac.Sum(nil)[:cursorMACLen]...)
	return base64.RawURLEncoding.EncodeToString(out)
}

// Decode verifies a cursor and rebuilds a pseudo item that compares equal to
// the last item of the previous page, so the next page starts strictly
// after it. dir is the folder being listed (for the path tie-break).
func (s *Signer) Decode(cur string, spec SortSpec, dir fsjail.VPath, c *collate.Collator) (*Item, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil || len(raw) <= cursorMACLen {
		return nil, ErrBadCursor
	}
	body, tag := raw[:len(raw)-cursorMACLen], raw[len(raw)-cursorMACLen:]
	mac := hmac.New(sha256.New, s.key)
	mac.Write(body)
	if !hmac.Equal(tag, mac.Sum(nil)[:cursorMACLen]) {
		return nil, ErrBadCursor
	}
	var p cursorPayload
	if err := json.Unmarshal(body, &p); err != nil || p.V != 1 {
		return nil, ErrBadCursor
	}
	if p.S != spec.String() {
		return nil, ErrBadCursor
	}
	it := &Item{Name: p.N, Pinned: p.P, IsDir: p.I, nameKey: NaturalKey(p.N, c)}
	if dir.IsRoot() {
		it.Path = "/" + p.N
	} else {
		it.Path = string(dir) + "/" + p.N
	}
	if err := applyKey(spec.Field, p.K, it); err != nil {
		return nil, ErrBadCursor
	}
	if err := applyKey(spec.Field2, p.K2, it); err != nil {
		return nil, ErrBadCursor
	}
	return it, nil
}
