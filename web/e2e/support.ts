/** Returns value, failing the test with a clear message when it is missing. */
export function defined<T>(value: T | null | undefined, what = 'value'): T {
  if (value === null || value === undefined) throw new Error(`expected ${what} to be present`)
  return value
}

/** The library directory ID in a page URL such as http://host/#/library/5. */
export function libraryDirectory(url: string): number {
  return Number(defined(/#\/library\/(\d+)/.exec(url)?.[1], 'library directory in URL'))
}
