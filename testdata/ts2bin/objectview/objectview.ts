interface ReadonlyValue {
  readonly value: number;
}

export function objectAlias(value: number): number {
  const object = { value };
  const alias = object;
  const view: ReadonlyValue = object;
  alias.value = alias.value + 1;
  return view.value;
}
