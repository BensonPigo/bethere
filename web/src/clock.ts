// sync-and-offline.md section 4: skew = serverTime - localNow, median of recent live samples.

export const SKEW_SAMPLES = 9;

export function median(values: number[]): number {
  if (values.length === 0) return 0;
  const s = [...values].sort((a, b) => a - b);
  const mid = s.length >> 1;
  return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2;
}

// Add one sample from a live response; cached snapshots must never be passed here.
export function addSkewSample(samples: number[], serverTimeMs: number, localNowMs: number): number[] {
  return [...samples, serverTimeMs - localNowMs].slice(-SKEW_SAMPLES);
}

export const currentSkew = (samples: number[]) => median(samples);

// Milliseconds until `startMs`; negative once overdue.
export function remainingMs(startMs: number, localNowMs: number, skewMs: number): number {
  return startMs - (localNowMs + skewMs);
}
