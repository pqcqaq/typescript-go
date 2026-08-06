export function readId<T extends { id: number }>(value: T): number { return value.id; }
