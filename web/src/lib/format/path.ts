/**
 * Returns the text of a display path from the server: the media root and a
 * missing directory have markers.
 */
export function displayPath(path: string): string {
  if (path === '/') return '整个媒体库'
  if (path === '/?') return '目录已缺失'
  return path
}
