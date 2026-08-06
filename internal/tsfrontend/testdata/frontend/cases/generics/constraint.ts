function lengthOf<T extends { length: number }>(value: T): number { return value.length; }
export const concreteLength: number = lengthOf({ length: 3 });
