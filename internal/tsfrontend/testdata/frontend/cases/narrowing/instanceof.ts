export function size(value: Date | string): number { return value instanceof Date ? value.getTime() : value.length; }
