class Base { value: number = 1; }
export class Derived extends Base { read(): number { return this.value; } }
