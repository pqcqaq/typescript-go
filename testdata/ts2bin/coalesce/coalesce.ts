export function coalesce(value: number | null | undefined, fallback: number): number {
  return value ?? fallback;
}
