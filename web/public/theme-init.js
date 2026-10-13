// Runs before first paint (classic blocking script in <head>, served from the
// same origin so the CSP needs no 'unsafe-inline'). Applies the stored theme
// and accent so the page never flashes the wrong colours (SPEC §9.1).
;(function () {
  try {
    var root = document.documentElement
    var pref = localStorage.getItem('theme')
    if (pref !== 'light' && pref !== 'dark') pref = 'system'
    var resolved = pref
    if (pref === 'system') {
      resolved =
        window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches
          ? 'dark'
          : 'light'
    }
    root.dataset.theme = resolved
    root.dataset.themePref = pref
    var accent = localStorage.getItem('accent')
    if (accent && /^[a-z]{3,12}$/.test(accent) && accent !== 'blue') {
      root.dataset.accent = accent
    }
    var density = localStorage.getItem('density')
    if (density === 'comfortable') root.dataset.density = density
  } catch (_e) {
    /* storage unavailable: keep defaults */
  }
})()
