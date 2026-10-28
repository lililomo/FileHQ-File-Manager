/** True when HTML may be previewed: the server serves user content from a
 *  separate, cookie-less origin (`config.contentBaseUrl`, or the public
 *  share meta for guests — Step 23e, SPEC ADR-06 (a)). */
export function htmlPreviewAvailable(contentBaseUrl: string | undefined): boolean {
  return !!contentBaseUrl
}
