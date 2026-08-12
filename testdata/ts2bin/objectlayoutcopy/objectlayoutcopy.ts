interface ReadonlyValue {
  readonly value: number;
}

export function objectLayoutCopy(value: number): number {
  const source = { value };
  const copy: ReadonlyValue = { value: source.value };
  source.value = source.value + 1;
  return copy.value;
}
