type IsString<T> = T extends string ? true : false;
export type Result = IsString<"value">;
