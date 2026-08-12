export function objectAlias(value: number): number {
  const object = { value };
  const alias = object;
  alias.value = alias.value + 1;
  return object.value;
}
