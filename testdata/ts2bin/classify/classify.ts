export function classify(value: number): number {
  if (value < 0) {
    return -1;
  }
  if (value < 1) {
    return 0;
  }
  return 1;
}
