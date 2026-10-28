/// <reference types="vite/client" />

/** True only in dev builds or when VITE_DESIGN_PAGE=1 at build time (vite `define`). */
declare const __DESIGN_PAGE__: boolean

/** hls.js ships no typings for its light build (no subtitles/alt-audio/EME);
 *  the API is the same as the full build (Step 23d, lazy HLS player). */
declare module 'hls.js/light' {
  export * from 'hls.js'
  export { default } from 'hls.js'
}
