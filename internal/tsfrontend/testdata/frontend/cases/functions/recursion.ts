export function factorial(value: number): number { return value <= 1 ? 1 : value * factorial(value - 1); }
