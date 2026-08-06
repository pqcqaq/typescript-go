export class Box { private current: number = 0; get value(): number { return this.current; } set value(next: number) { this.current = next; } }
