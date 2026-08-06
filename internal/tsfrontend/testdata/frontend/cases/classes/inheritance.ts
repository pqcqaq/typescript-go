class Base { value(): number { return 1; } }
export class Derived extends Base { override value(): number { return super.value() + 1; } }
