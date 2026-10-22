package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Branding logo (SPEC §8.17 "nama & logo aplikasi"): one PNG, at most
// MaxLogoBytes, verified by magic bytes, stored as DATA_DIR/branding/logo.png
// and served by GET /api/v1/branding/logo (public: the login page shows it).
const (
	LogoName     = "logo.png"
	MaxLogoBytes = 512 << 10
	brandingDir  = "branding"
)

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// SaveLogo validates and stores the PNG, then records branding.logo.
func (s *Service) SaveLogo(ctx context.Context, a Actor, r io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(r, MaxLogoBytes+1))
	if err != nil {
		return internalErr(err)
	}
	if len(data) > MaxLogoBytes {
		return newErr("PAYLOAD_TOO_LARGE", http.StatusRequestEntityTooLarge, "logo must be at most 512 KB", map[string]any{"limit": MaxLogoBytes})
	}
	if len(data) < len(pngMagic)+16 || !bytes.HasPrefix(data, pngMagic) {
		return newErr("UNSUPPORTED_TYPE", http.StatusUnsupportedMediaType, "logo must be a PNG image", map[string]any{"fields": map[string]any{"logo": "png_required"}})
	}
	sys, err := fsjail.OpenSys(s.cfg.DataDir, brandingDir)
	if err != nil {
		return internalErr(err)
	}
	defer func() { _ = sys.Close() }()
	tmp := LogoName + ".tmp"
	_ = sys.Remove(tmp)
	f, err := sys.Create(tmp)
	if err != nil {
		return internalErr(err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = sys.Remove(tmp)
		return internalErr(err)
	}
	if err := f.Close(); err != nil {
		_ = sys.Remove(tmp)
		return internalErr(err)
	}
	if err := sys.Rename(tmp, LogoName); err != nil {
		_ = sys.Remove(tmp)
		return internalErr(err)
	}
	if err := s.setBrandingLogo(ctx, a, LogoName); err != nil {
		return internalErr(err)
	}
	return nil
}

// DeleteLogo removes the logo and the marker.
func (s *Service) DeleteLogo(ctx context.Context, a Actor) error {
	sys, err := fsjail.OpenSys(s.cfg.DataDir, brandingDir)
	if err != nil {
		return internalErr(err)
	}
	defer func() { _ = sys.Close() }()
	if err := sys.Remove(LogoName); err != nil && !errors.Is(err, fsjail.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
		return internalErr(err)
	}
	if err := s.setBrandingLogo(ctx, a, ""); err != nil {
		return internalErr(err)
	}
	return nil
}

// OpenLogo opens the stored logo for serving; ErrNoLogo when none.
func (s *Service) OpenLogo() (*os.File, os.FileInfo, error) {
	if s.cfg.Runtime().BrandingLogo == "" {
		return nil, nil, ErrNoLogo
	}
	sys, err := fsjail.OpenSys(s.cfg.DataDir, brandingDir)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = sys.Close() }()
	f, err := sys.Open(LogoName)
	if err != nil {
		if errors.Is(err, fsjail.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrNoLogo
		}
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// ErrNoLogo means no custom logo is stored.
var ErrNoLogo = errors.New("admin: no logo")
