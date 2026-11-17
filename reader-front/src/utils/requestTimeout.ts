export async function withRequestTimeout<T>(
  parent: AbortSignal,
  milliseconds: number,
  run: (signal: AbortSignal) => Promise<T>,
): Promise<T> {
  if (typeof AbortSignal.any === 'function' && typeof AbortSignal.timeout === 'function') {
    return run(AbortSignal.any([parent, AbortSignal.timeout(milliseconds)]));
  }

  const controller = new AbortController();
  const abort = () => controller.abort();
  if (parent.aborted) abort();
  else parent.addEventListener('abort', abort, { once: true });
  const timer = setTimeout(abort, milliseconds);
  try {
    return await run(controller.signal);
  } finally {
    clearTimeout(timer);
    parent.removeEventListener('abort', abort);
  }
}
