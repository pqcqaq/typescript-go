export function coalesceAssign(value: number | null | undefined, fallback: number): number {
  value ??= fallback;
  return value;
}
