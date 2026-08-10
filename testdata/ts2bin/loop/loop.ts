export function compute(step: number, limit: number): number {
  let value = step;
  while (value < limit) {
    value = value + step;
  }
  return value;
}
