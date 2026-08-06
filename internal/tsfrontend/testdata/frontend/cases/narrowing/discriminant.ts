type Item = { kind: "text"; value: string } | { kind: "count"; value: number };
export function read(item: Item): string | number { return item.kind === "text" ? item.value : item.value; }
