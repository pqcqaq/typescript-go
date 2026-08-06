function isString(value: unknown): value is string { return typeof value === "string"; }
export function size(value: unknown): number { return isString(value) ? value.length : 0; }
