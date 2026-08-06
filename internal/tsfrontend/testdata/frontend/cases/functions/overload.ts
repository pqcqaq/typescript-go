export function select(value: string): string;
export function select(value: number): number;
export function select(value: string | number): string | number { return value; }
export const selected: number = select(1);
