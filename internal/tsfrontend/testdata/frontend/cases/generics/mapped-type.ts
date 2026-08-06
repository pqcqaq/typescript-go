type ReadonlyRecord<T> = { readonly [Key in keyof T]: T[Key] };
export type Point = ReadonlyRecord<{ x: number; y: number }>;
