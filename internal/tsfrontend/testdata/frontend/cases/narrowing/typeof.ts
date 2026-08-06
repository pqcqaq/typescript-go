export function stringify(value: string | number): string { return typeof value === "string" ? value : value.toString(); }
