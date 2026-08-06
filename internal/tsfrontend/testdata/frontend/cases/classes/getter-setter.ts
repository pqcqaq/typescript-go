export class Meter { private current = 0; get value(): number { return this.current; } set value(next: number) { this.current = next; } }
