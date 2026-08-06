export type ReadonlyPair<T> = { readonly [K in keyof T]: T[K] };
