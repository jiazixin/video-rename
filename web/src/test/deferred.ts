/** A controlled promise for cancellation and stale-response tests. */
export function deferred<T>() {
  let resolve: (value: T) => void = () => {
    throw new Error('promise is not initialized')
  }
  let reject: (reason: unknown) => void = () => {
    throw new Error('promise is not initialized')
  }
  const promise = new Promise<T>((done, fail) => {
    resolve = done
    reject = fail
  })
  return { promise, resolve, reject }
}
