type Value = { text: string } | { count: number };
export function read(value: Value): string | number { return "text" in value ? value.text : value.count; }
