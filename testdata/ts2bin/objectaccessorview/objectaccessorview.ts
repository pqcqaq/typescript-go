interface ReadonlyResult {
  get result(): number | null | undefined;
}

export function propertyNullishAssign(value: number | null | undefined): number {
  const object = {
    backing: value,
    get result(): number | null | undefined {
      return this.backing;
    },
    set result(next: number) {
      this.backing = next;
    },
  };
  const view: ReadonlyResult = object;
  const key = "result" as const;
  return (object[key] ??= 1);
}
